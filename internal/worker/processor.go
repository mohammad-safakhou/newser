package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/store"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	// Streams used by the worker processor.
	StreamRunEnqueued  = "run.enqueued"
	StreamTaskDispatch = "task.dispatch"

	stageRunReceived   = "run.enqueued"
	stageBootstrapTask = "bootstrap.dispatch"
)

// StoreAPI captures the store methods required by the worker.
type StoreAPI interface {
	ClaimIdempotency(ctx context.Context, scope, key string) (bool, error)
	UpsertCheckpoint(ctx context.Context, cp store.Checkpoint) error
	GetCheckpoint(ctx context.Context, runID, stage string) (store.Checkpoint, bool, error)
	ListCheckpointsByStatus(ctx context.Context, statuses ...string) ([]store.Checkpoint, error)
	MarkCheckpointStatus(ctx context.Context, runID, stage, status string) error
	CreateRun(ctx context.Context, topicID string, status string) (string, error)
}

// Processor drives run execution by consuming run.enqueued events and checkpointing progress.
type Processor struct {
	logger       *log.Logger
	store        StoreAPI
	consumer     *streams.Consumer
	publisher    *streams.Publisher
	runStream    string
	taskStream   string
	tracer       trace.Tracer
	runCounter   otelmetric.Int64Counter
	taskCounter  otelmetric.Int64Counter
	retryCounter otelmetric.Int64Counter
}

// RunEnqueuedPayload mirrors the JSON payload published to run.enqueued.
type RunEnqueuedPayload struct {
	RunID       string                 `json:"run_id"`
	TopicID     string                 `json:"topic_id"`
	UserID      string                 `json:"user_id"`
	Trigger     string                 `json:"trigger"`
	Preferences map[string]interface{} `json:"preferences_snapshot"`
	Context     map[string]interface{} `json:"context_snapshot"`
}

// NewProcessor constructs a Processor.
func NewProcessor(logger *log.Logger, st StoreAPI, pub *streams.Publisher, cons *streams.Consumer, runStream, taskStream string, meter otelmetric.Meter, tracer trace.Tracer) *Processor {
	if tracer == nil {
		tracer = trace.NewNoopTracerProvider().Tracer("worker")
	}

	proc := &Processor{
		logger:     logger,
		store:      st,
		consumer:   cons,
		publisher:  pub,
		runStream:  runStream,
		taskStream: taskStream,
		tracer:     tracer,
	}
	if meter != nil {
		var err error
		proc.runCounter, err = meter.Int64Counter("worker_runs_processed")
		if err != nil {
			logger.Printf("warn: create run counter failed: %v", err)
		}
		proc.taskCounter, err = meter.Int64Counter("worker_tasks_dispatched")
		if err != nil {
			logger.Printf("warn: create task counter failed: %v", err)
		}
		proc.retryCounter, err = meter.Int64Counter("worker_checkpoint_retries")
		if err != nil {
			logger.Printf("warn: create retry counter failed: %v", err)
		}
	}
	return proc
}

// Start blocks, continuously processing run.enqueued events until the context is cancelled.
func (p *Processor) Start(ctx context.Context) error {
	p.logger.Printf("worker processor starting; consuming stream %s", p.runStream)
	if err := p.resumePending(ctx); err != nil {
		p.logger.Printf("warn: resume pending checkpoints failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			p.logger.Printf("worker processor stopping: %v", ctx.Err())
			return nil
		default:
		}

		msgs, err := p.consumer.Read(ctx, p.runStream, streams.WithBlock(5*time.Second), streams.WithCount(16))
		if err != nil {
			p.logger.Printf("error reading stream: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		for _, msg := range msgs {
			if err := p.handleRunEnqueued(ctx, msg); err != nil {
				p.logger.Printf("error handling run message %s: %v", msg.ID, err)
			}
			if err := p.consumer.Ack(ctx, p.runStream, msg.ID); err != nil {
				p.logger.Printf("warn: failed to ack message %s: %v", msg.ID, err)
			}
		}
	}
}

func (p *Processor) handleRunEnqueued(ctx context.Context, msg streams.Message) error {
	ctx, span := p.tracer.Start(ctx, "worker.handle_run")
	defer span.End()

	claimed, err := p.store.ClaimIdempotency(ctx, msg.Envelope.EventType, msg.Envelope.EventID)
	if err != nil {
		return fmt.Errorf("claim idempotency: %w", err)
	}
	if !claimed {
		p.logger.Printf("skip event %s — already processed", msg.Envelope.EventID)
		return nil
	}

	var payload RunEnqueuedPayload
	if err := json.Unmarshal(msg.Envelope.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal run payload: %w", err)
	}

	runID := payload.RunID
	if runID == "" {
		generated, err := p.store.CreateRun(ctx, payload.TopicID, "pending")
		if err != nil {
			return fmt.Errorf("create run: %w", err)
		}
		runID = generated
	}

	checkpointToken := fmt.Sprintf("%s:%s", msg.Envelope.EventID, runID)
	if err := p.store.UpsertCheckpoint(ctx, store.Checkpoint{
		RunID:           runID,
		Stage:           stageRunReceived,
		Status:          store.CheckpointStatusReceived,
		CheckpointToken: checkpointToken,
		Payload: map[string]interface{}{
			"topic_id":    payload.TopicID,
			"user_id":     payload.UserID,
			"trigger":     payload.Trigger,
			"preferences": payload.Preferences,
			"context":     payload.Context,
		},
	}); err != nil {
		return fmt.Errorf("upsert run checkpoint: %w", err)
	}

	if err := p.dispatchBootstrap(ctx, runID, payload); err != nil {
		return err
	}
	if p.runCounter != nil {
		p.runCounter.Add(ctx, 1)
	}
	return nil
}

func (p *Processor) dispatchBootstrap(ctx context.Context, runID string, payload RunEnqueuedPayload) error {
	cp, exists, err := p.store.GetCheckpoint(ctx, runID, stageBootstrapTask)
	if err != nil {
		return fmt.Errorf("get bootstrap checkpoint: %w", err)
	}
	if exists && cp.Status == store.CheckpointStatusDispatched {
		// already dispatched; nothing to do
		return nil
	}

	taskID := fmt.Sprintf("bootstrap:%s", runID)
	dispatchToken := fmt.Sprintf("%s:%s", stageBootstrapTask, runID)
	taskPayload := map[string]interface{}{
		"run_id":           runID,
		"task_id":          taskID,
		"task_type":        "bootstrap",
		"priority":         1,
		"plan_snapshot":    map[string]interface{}{"kind": "bootstrap"},
		"parameters":       map[string]interface{}{"context": payload.Context, "preferences": payload.Preferences},
		"checkpoint_token": dispatchToken,
	}

	if err := p.store.UpsertCheckpoint(ctx, store.Checkpoint{
		RunID:           runID,
		Stage:           stageBootstrapTask,
		Status:          store.CheckpointStatusDispatched,
		CheckpointToken: dispatchToken,
		Payload:         taskPayload,
		Retries: func() int {
			if exists {
				return cp.Retries + 1
			}
			return 0
		}(),
	}); err != nil {
		return fmt.Errorf("upsert bootstrap checkpoint: %w", err)
	}

	if _, err := p.publisher.PublishRaw(ctx, p.taskStream, StreamTaskDispatch, "v1", taskPayload); err != nil {
		return fmt.Errorf("publish task.dispatch: %w", err)
	}
	if p.taskCounter != nil {
		p.taskCounter.Add(ctx, 1)
	}
	return nil
}

func (p *Processor) resumePending(ctx context.Context) error {
	checkpoints, err := p.store.ListCheckpointsByStatus(ctx, store.CheckpointStatusDispatched)
	if err != nil {
		return err
	}
	for _, cp := range checkpoints {
		if cp.Stage != stageBootstrapTask {
			continue
		}
		if cp.Payload == nil {
			continue
		}
		if _, err := p.publisher.PublishRaw(ctx, p.taskStream, StreamTaskDispatch, "v1", cp.Payload); err != nil {
			p.logger.Printf("warn: failed to re-dispatch task for run %s: %v", cp.RunID, err)
			continue
		}
		// increment retry counter
		cp.Retries++
		if err := p.store.UpsertCheckpoint(ctx, cp); err != nil {
			p.logger.Printf("warn: failed to bump retry count for run %s: %v", cp.RunID, err)
		}
		if p.retryCounter != nil {
			p.retryCounter.Add(ctx, 1)
		}
	}
	return nil
}
