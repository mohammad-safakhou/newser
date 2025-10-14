package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/executor"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/store"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	// Streams used by the worker processor.
	StreamRunEnqueued             = "run.enqueued"
	StreamTaskDispatch            = "task.dispatch"
	StreamBudgetApprovalRequested = "budget.approval.requested"

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
	GetTopicBudgetConfig(ctx context.Context, topicID string) (budget.Config, bool, error)
	GetPendingBudgetApproval(ctx context.Context, topicID string) (store.BudgetApprovalRecord, bool, error)
	MarkRunPendingApproval(ctx context.Context, runID string) error
	CreateBudgetApproval(ctx context.Context, runID, topicID, requestedBy string, estimatedCost, threshold float64) error
}

// ProcessorOption configures processor runtime behaviour.
type ProcessorOption func(*Processor)

// WithMaxConcurrency limits the number of concurrent run handlers.
func WithMaxConcurrency(n int) ProcessorOption {
	return func(p *Processor) {
		if n > 1 {
			p.maxConcurrency = n
		} else {
			p.maxConcurrency = 1
		}
	}
}

// WithReadBatchSize adjusts the Redis read batch size per poll iteration.
func WithReadBatchSize(n int64) ProcessorOption {
	return func(p *Processor) {
		if n > 0 {
			p.readBatchSize = n
		}
	}
}

// WithResumeInterval enables periodic checkpoint reclamation.
func WithResumeInterval(interval time.Duration) ProcessorOption {
	return func(p *Processor) {
		if interval > 0 {
			p.resumeInterval = interval
		} else {
			p.resumeInterval = 0
		}
	}
}

// WithBackpressureThreshold pauses ingestion when pending entries exceed the threshold.
func WithBackpressureThreshold(pending int64, sleep time.Duration) ProcessorOption {
	return func(p *Processor) {
		if pending > 0 {
			p.backpressureThreshold = pending
			if sleep > 0 {
				p.backpressureSleep = sleep
			}
		} else {
			p.backpressureThreshold = 0
		}
	}
}

type eventPublisher interface {
	PublishRaw(ctx context.Context, stream, eventType, version string, payload interface{}, opts ...streams.PublishOption) (string, error)
}

// ResumeStats captures summary information when replaying checkpoints.
type ResumeStats struct {
	Checkpoints        int
	Reclaimed          int
	SkippedForApproval int
}

// Processor drives run execution by consuming run.enqueued events and checkpointing progress.
type Processor struct {
	logger                *log.Logger
	store                 StoreAPI
	consumer              *streams.Consumer
	publisher             eventPublisher
	runStream             string
	taskStream            string
	tracer                trace.Tracer
	runCounter            otelmetric.Int64Counter
	taskCounter           otelmetric.Int64Counter
	retryCounter          otelmetric.Int64Counter
	taskDuration          otelmetric.Float64Histogram
	executor              *executor.Executor
	runner                executor.TaskRunner
	lagHistogram          otelmetric.Float64Histogram
	lagPendingHistogram   otelmetric.Float64Histogram
	lagIdleHistogram      otelmetric.Float64Histogram
	checkpointPending     otelmetric.Float64Histogram
	checkpointAgeSeconds  otelmetric.Float64Histogram
	maxConcurrency        int
	readBatchSize         int64
	resumeInterval        time.Duration
	backpressureThreshold int64
	backpressureSleep     time.Duration
	sem                   chan struct{}
	resumeMu              sync.Mutex
	wg                    sync.WaitGroup
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

func (p *Processor) enforceBudgetGate(ctx context.Context, runID string, payload RunEnqueuedPayload) (bool, error) {
	if p.store == nil || payload.TopicID == "" {
		return false, nil
	}
	cfg, ok, err := p.store.GetTopicBudgetConfig(ctx, payload.TopicID)
	if err != nil {
		return false, fmt.Errorf("load budget config: %w", err)
	}
	if !ok || cfg.IsZero() {
		return false, nil
	}
	if !cfg.RequireApproval {
		return false, nil
	}
	if err := p.store.MarkRunPendingApproval(ctx, runID); err != nil {
		return true, fmt.Errorf("mark run pending approval: %w", err)
	}
	threshold := 0.0
	if cfg.ApprovalThreshold != nil {
		threshold = *cfg.ApprovalThreshold
	}
	estimatedCost := 0.0
	requestedBy := payload.UserID
	if strings.TrimSpace(requestedBy) == "" {
		requestedBy = "system"
	}
	if existing, ok, err := p.store.GetPendingBudgetApproval(ctx, payload.TopicID); err == nil && ok {
		if existing.RunID != runID {
			p.logger.Printf("budget approval already pending for topic %s (run %s)", payload.TopicID, existing.RunID)
		}
		return true, nil
	}
	if err := p.store.CreateBudgetApproval(ctx, runID, payload.TopicID, requestedBy, estimatedCost, threshold); err != nil {
		return true, fmt.Errorf("create budget approval: %w", err)
	}
	if p.publisher != nil {
		event := map[string]interface{}{
			"run_id":           runID,
			"topic_id":         payload.TopicID,
			"requested_by":     requestedBy,
			"estimated_cost":   estimatedCost,
			"threshold":        threshold,
			"require_approval": cfg.RequireApproval,
			"created_at":       time.Now().UTC().Format(time.RFC3339),
		}
		if _, err := p.publisher.PublishRaw(ctx, StreamBudgetApprovalRequested, StreamBudgetApprovalRequested, "v1", event); err != nil {
			p.logger.Printf("warn: publish budget approval event failed: %v", err)
		}
	}
	return true, nil
}

// NewProcessor constructs a Processor.
func NewProcessor(logger *log.Logger, st StoreAPI, pub eventPublisher, cons *streams.Consumer, runStream, taskStream string, meter otelmetric.Meter, tracer trace.Tracer, opts ...ProcessorOption) *Processor {
	if tracer == nil {
		tracer = trace.NewNoopTracerProvider().Tracer("worker")
	}

	proc := &Processor{
		logger:            logger,
		store:             st,
		consumer:          cons,
		publisher:         pub,
		runStream:         runStream,
		taskStream:        taskStream,
		tracer:            tracer,
		maxConcurrency:    1,
		readBatchSize:     16,
		backpressureSleep: 5 * time.Second,
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
		proc.lagHistogram, err = meter.Float64Histogram("worker_stream_lag", otelmetric.WithDescription("Approximate lag for worker consumer"), otelmetric.WithUnit("1"))
		if err != nil {
			logger.Printf("warn: create lag histogram failed: %v", err)
		}
		proc.lagPendingHistogram, err = meter.Float64Histogram("worker_stream_pending", otelmetric.WithDescription("Pending entries for worker consumer"), otelmetric.WithUnit("1"))
		if err != nil {
			logger.Printf("warn: create pending histogram failed: %v", err)
		}
		proc.lagIdleHistogram, err = meter.Float64Histogram("worker_stream_oldest_idle_seconds", otelmetric.WithDescription("Oldest pending entry idle time"), otelmetric.WithUnit("s"))
		if err != nil {
			logger.Printf("warn: create idle histogram failed: %v", err)
		}
		proc.checkpointPending, err = meter.Float64Histogram("worker_checkpoint_pending", otelmetric.WithDescription("Pending checkpoint count at resume"), otelmetric.WithUnit("1"))
		if err != nil {
			logger.Printf("warn: create checkpoint pending histogram failed: %v", err)
		}
		proc.checkpointAgeSeconds, err = meter.Float64Histogram("worker_checkpoint_age_seconds", otelmetric.WithDescription("Age of checkpoint when resuming"), otelmetric.WithUnit("s"))
		if err != nil {
			logger.Printf("warn: create checkpoint age histogram failed: %v", err)
		}
		proc.taskDuration, err = meter.Float64Histogram("worker_task_duration_seconds", otelmetric.WithDescription("Duration of executor tasks dispatched by worker"), otelmetric.WithUnit("s"))
		if err != nil {
			logger.Printf("warn: create task duration histogram failed: %v", err)
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(proc)
		}
	}
	if proc.maxConcurrency < 1 {
		proc.maxConcurrency = 1
	}
	if proc.readBatchSize <= 0 {
		proc.readBatchSize = 16
	}
	if proc.backpressureSleep <= 0 {
		proc.backpressureSleep = 5 * time.Second
	}
	if proc.maxConcurrency > 1 && proc.sem == nil {
		proc.sem = make(chan struct{}, proc.maxConcurrency)
	}

	metrics := executor.Metrics{}
	if proc.retryCounter != nil {
		metrics.RetryCounter = proc.onTaskRetry
	}
	if proc.taskDuration != nil {
		metrics.Duration = proc.onTaskDuration
	}

	proc.executor = executor.New(
		executor.WithCheckpointManager(executor.NewStoreCheckpointManager(st)),
		executor.WithMetrics(metrics),
	)
	proc.runner = &taskDispatchRunner{publisher: pub, taskStream: taskStream, counter: proc.taskCounter}
	return proc
}

// Start blocks, continuously processing run.enqueued events until the context is cancelled.
func (p *Processor) Start(ctx context.Context) error {
	p.logger.Printf("worker processor starting; consuming stream %s", p.runStream)
	if stats, err := p.ResumePending(ctx); err != nil {
		p.logger.Printf("warn: resume pending checkpoints failed: %v", err)
	} else if stats.Checkpoints > 0 || stats.Reclaimed > 0 || stats.SkippedForApproval > 0 {
		p.logger.Printf("resume summary: checkpoints=%d reclaimed=%d skipped_for_approval=%d", stats.Checkpoints, stats.Reclaimed, stats.SkippedForApproval)
	}
	p.recordLagMetrics(ctx)
	lagTicker := time.NewTicker(30 * time.Second)
	defer lagTicker.Stop()

	var resumeTicker *time.Ticker
	var resumeCh <-chan time.Time
	if p.resumeInterval > 0 {
		resumeTicker = time.NewTicker(p.resumeInterval)
		resumeCh = resumeTicker.C
		defer resumeTicker.Stop()
	}

	blockOpt := streams.WithBlock(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			inflight := 0
			if p.sem != nil {
				inflight = len(p.sem)
			}
			p.logger.Printf("worker processor stopping: %v (inflight=%d)", ctx.Err(), inflight)
			p.wg.Wait()
			return nil
		case <-lagTicker.C:
			p.recordLagMetrics(ctx)
			continue
		case <-resumeCh:
			if stats, err := p.ResumePending(ctx); err != nil {
				p.logger.Printf("warn: periodic resume failed: %v", err)
			} else if stats.Checkpoints > 0 || stats.Reclaimed > 0 || stats.SkippedForApproval > 0 {
				p.logger.Printf("periodic resume summary: checkpoints=%d reclaimed=%d skipped_for_approval=%d", stats.Checkpoints, stats.Reclaimed, stats.SkippedForApproval)
			}
			continue
		default:
		}

		if p.backpressureThreshold > 0 {
			metrics, err := p.consumer.LagMetrics(ctx, p.runStream)
			if err != nil {
				p.logger.Printf("warn: backpressure metrics failed: %v", err)
			} else if metrics.Pending >= p.backpressureThreshold {
				p.logger.Printf("backpressure engaged: pending=%d threshold=%d; sleeping %s", metrics.Pending, p.backpressureThreshold, p.backpressureSleep)
				timer := time.NewTimer(p.backpressureSleep)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					continue
				case <-timer.C:
				}
				continue
			}
		}

		opts := []streams.ConsumerOption{blockOpt}
		if p.readBatchSize > 0 {
			opts = append(opts, streams.WithCount(p.readBatchSize))
		}

		msgs, err := p.consumer.Read(ctx, p.runStream, opts...)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			p.logger.Printf("error reading stream: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		concurrent := p.maxConcurrency > 1 && p.sem != nil
		for _, msg := range msgs {
			if concurrent {
				select {
				case p.sem <- struct{}{}:
					p.wg.Add(1)
					go p.processMessage(ctx, msg)
				case <-ctx.Done():
					p.wg.Wait()
					return nil
				}
			} else {
				p.processMessageSync(ctx, msg)
			}
		}
	}
}

func (p *Processor) recordLagMetrics(ctx context.Context) {
	if p.lagHistogram == nil && p.lagPendingHistogram == nil && p.lagIdleHistogram == nil {
		return
	}
	metrics, err := p.consumer.LagMetrics(ctx, p.runStream)
	if err != nil {
		p.logger.Printf("warn: collect stream lag metrics failed: %v", err)
		return
	}
	if p.lagHistogram != nil && metrics.Lag >= 0 {
		p.lagHistogram.Record(ctx, float64(metrics.Lag))
	}
	if p.lagPendingHistogram != nil {
		p.lagPendingHistogram.Record(ctx, float64(metrics.Pending))
	}
	if p.lagIdleHistogram != nil && metrics.OldestIdle > 0 {
		p.lagIdleHistogram.Record(ctx, metrics.OldestIdle.Seconds())
	}
}

func (p *Processor) onTaskDuration(ctx context.Context, task executor.Task, dur time.Duration) {
	if p.taskDuration == nil || dur <= 0 {
		return
	}
	stage := task.Stage
	if stage == "" {
		stage = task.ID
	}
	attrs := []attribute.KeyValue{attribute.String("task.stage", stage)}
	p.taskDuration.Record(ctx, dur.Seconds(), otelmetric.WithAttributes(attrs...))
}

func (p *Processor) onTaskRetry(ctx context.Context, task executor.Task, attempt int) {
	if p.retryCounter == nil {
		return
	}
	stage := task.Stage
	if stage == "" {
		stage = task.ID
	}
	attrs := []attribute.KeyValue{attribute.String("task.stage", stage)}
	if attempt > 0 {
		attrs = append(attrs, attribute.Int64("task.attempt", int64(attempt)))
	}
	p.retryCounter.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
}

func (p *Processor) processMessage(ctx context.Context, msg streams.Message) {
	defer func() {
		if p.sem != nil {
			<-p.sem
		}
		p.wg.Done()
	}()
	p.processMessageSync(ctx, msg)
}

func (p *Processor) processMessageSync(ctx context.Context, msg streams.Message) {
	if err := p.handleRunEnqueued(ctx, msg); err != nil {
		p.logger.Printf("error handling run message %s: %v", msg.ID, err)
	}
	if err := p.consumer.Ack(ctx, p.runStream, msg.ID); err != nil {
		p.logger.Printf("warn: failed to ack message %s: %v", msg.ID, err)
	}
}

func (p *Processor) buildResumePayload(ctx context.Context, runID string, dispatch map[string]interface{}) RunEnqueuedPayload {
	payload := RunEnqueuedPayload{RunID: runID}
	if params, ok := dispatch["parameters"].(map[string]interface{}); ok {
		if ctxSnapshot, ok := params["context"].(map[string]interface{}); ok {
			payload.Context = ctxSnapshot
			if topic, ok := ctxSnapshot["topic_id"].(string); ok {
				payload.TopicID = topic
			}
			if user, ok := ctxSnapshot["user_id"].(string); ok {
				payload.UserID = user
			}
		}
		if prefs, ok := params["preferences"].(map[string]interface{}); ok {
			payload.Preferences = prefs
		}
	}
	if p.store == nil {
		return payload
	}
	cp, ok, err := p.store.GetCheckpoint(ctx, runID, stageRunReceived)
	if err != nil {
		p.logger.Printf("warn: load run checkpoint for %s failed: %v", runID, err)
		return payload
	}
	if !ok || cp.Payload == nil {
		return payload
	}
	if topic, ok := cp.Payload["topic_id"].(string); ok {
		payload.TopicID = topic
	}
	if user, ok := cp.Payload["user_id"].(string); ok {
		payload.UserID = user
	}
	if trigger, ok := cp.Payload["trigger"].(string); ok {
		payload.Trigger = trigger
	}
	if prefs, ok := cp.Payload["preferences"].(map[string]interface{}); ok {
		payload.Preferences = prefs
	}
	if ctxSnapshot, ok := cp.Payload["context"].(map[string]interface{}); ok {
		payload.Context = ctxSnapshot
	}
	return payload
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

	skipDispatch, err := p.enforceBudgetGate(ctx, runID, payload)
	if err != nil {
		return err
	}
	if skipDispatch {
		return nil
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

	dispatchToken := fmt.Sprintf("bootstrap:%s:%s", msg.Envelope.EventID, runID)
	taskPayload := map[string]interface{}{
		"run_id":           runID,
		"task_id":          fmt.Sprintf("bootstrap:%s", runID),
		"task_type":        "bootstrap",
		"priority":         1,
		"plan_snapshot":    map[string]interface{}{"kind": "bootstrap"},
		"parameters":       map[string]interface{}{"context": payload.Context, "preferences": payload.Preferences},
		"checkpoint_token": dispatchToken,
	}

	graph := executor.Graph{Tasks: map[string]executor.Task{
		stageBootstrapTask: {
			ID:              stageBootstrapTask,
			Stage:           stageBootstrapTask,
			Payload:         map[string]interface{}{"dispatch": taskPayload},
			CheckpointToken: dispatchToken,
		},
	}}

	if _, err := p.executor.Execute(ctx, runID, graph, p.runner); err != nil {
		return fmt.Errorf("execute bootstrap: %w", err)
	}
	if p.runCounter != nil {
		p.runCounter.Add(ctx, 1)
	}
	return nil
}

func (p *Processor) ResumePending(ctx context.Context) (ResumeStats, error) {
	p.resumeMu.Lock()
	defer p.resumeMu.Unlock()
	stats := ResumeStats{}
	checkpoints, err := p.store.ListCheckpointsByStatus(ctx, store.CheckpointStatusDispatched)
	if err != nil {
		return stats, err
	}
	if p.checkpointPending != nil {
		p.checkpointPending.Record(ctx, float64(len(checkpoints)))
	}
	for _, cp := range checkpoints {
		if cp.Stage != stageBootstrapTask {
			continue
		}
		if cp.Payload == nil {
			continue
		}
		if p.checkpointAgeSeconds != nil && !cp.UpdatedAt.IsZero() {
			age := time.Since(cp.UpdatedAt)
			if age > 0 {
				p.checkpointAgeSeconds.Record(ctx, age.Seconds())
			}
		}
		payloadWrapper, ok := cp.Payload["payload"].(map[string]interface{})
		if !ok {
			p.logger.Printf("warn: missing payload wrapper for run %s", cp.RunID)
			continue
		}
		dispatchPayload, ok := payloadWrapper["dispatch"].(map[string]interface{})
		if !ok {
			p.logger.Printf("warn: missing dispatch payload for run %s", cp.RunID)
			continue
		}
		runPayload := p.buildResumePayload(ctx, cp.RunID, dispatchPayload)
		skipDispatch, err := p.enforceBudgetGate(ctx, cp.RunID, runPayload)
		if err != nil {
			p.logger.Printf("warn: budget gate on resume for run %s: %v", cp.RunID, err)
			continue
		}
		if skipDispatch {
			stats.SkippedForApproval++
			if err := p.store.MarkCheckpointStatus(ctx, cp.RunID, cp.Stage, store.CheckpointStatusCompleted); err != nil {
				p.logger.Printf("warn: mark checkpoint complete for approval run %s failed: %v", cp.RunID, err)
			}
			continue
		}
		if _, err := p.publisher.PublishRaw(ctx, p.taskStream, StreamTaskDispatch, "v1", dispatchPayload); err != nil {
			p.logger.Printf("warn: failed to re-dispatch task for run %s: %v", cp.RunID, err)
			continue
		}
		stats.Checkpoints++
		cp.Retries++
		if err := p.store.UpsertCheckpoint(ctx, cp); err != nil {
			p.logger.Printf("warn: failed to bump retry count for run %s: %v", cp.RunID, err)
		}
		if p.retryCounter != nil {
			p.retryCounter.Add(ctx, 1)
		}
	}

	if p.consumer == nil {
		return stats, nil
	}

	const claimBatch int64 = 50
	next := "0-0"
	for {
		claimed, nextID, err := p.consumer.AutoClaim(ctx, p.runStream, 5*time.Minute, next, claimBatch)
		if err != nil {
			p.logger.Printf("warn: auto-claim pending entries failed: %v", err)
			break
		}
		if len(claimed) == 0 {
			if nextID == "" || nextID == "0-0" {
				break
			}
			next = nextID
			continue
		}
		for _, msg := range claimed {
			if err := p.handleRunEnqueued(ctx, msg); err != nil {
				p.logger.Printf("warn: handle reclaimed run %s: %v", msg.ID, err)
			}
			if err := p.consumer.Ack(ctx, p.runStream, msg.ID); err != nil {
				p.logger.Printf("warn: ack reclaimed message %s failed: %v", msg.ID, err)
			}
			stats.Reclaimed++
		}
		if nextID == "" || nextID == "0-0" || len(claimed) < int(claimBatch) {
			break
		}
		next = nextID
	}
	return stats, nil
}
