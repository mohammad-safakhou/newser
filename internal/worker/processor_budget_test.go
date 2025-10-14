package worker

import (
	"context"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type budgetStoreStub struct {
	budgetConfig budget.Config
	budgetExists bool
	budgetErr    error
	pendingRunID string
	pendingErr   error
	approvalArgs struct {
		runID       string
		topicID     string
		requestedBy string
		estimated   float64
		threshold   float64
	}
	approvalErr error
}

func (s *budgetStoreStub) ClaimIdempotency(context.Context, string, string) (bool, error) {
	return true, nil
}
func (s *budgetStoreStub) UpsertCheckpoint(context.Context, store.Checkpoint) error { return nil }
func (s *budgetStoreStub) GetCheckpoint(context.Context, string, string) (store.Checkpoint, bool, error) {
	return store.Checkpoint{}, false, nil
}
func (s *budgetStoreStub) ListCheckpointsByStatus(context.Context, ...string) ([]store.Checkpoint, error) {
	return nil, nil
}
func (s *budgetStoreStub) MarkCheckpointStatus(context.Context, string, string, string) error {
	return nil
}
func (s *budgetStoreStub) CreateRun(context.Context, string, string) (string, error) { return "", nil }
func (s *budgetStoreStub) GetTopicBudgetConfig(context.Context, string) (budget.Config, bool, error) {
	return s.budgetConfig, s.budgetExists, s.budgetErr
}
func (s *budgetStoreStub) GetPendingBudgetApproval(context.Context, string) (store.BudgetApprovalRecord, bool, error) {
	return store.BudgetApprovalRecord{}, false, nil
}
func (s *budgetStoreStub) MarkRunPendingApproval(_ context.Context, runID string) error {
	s.pendingRunID = runID
	return s.pendingErr
}
func (s *budgetStoreStub) CreateBudgetApproval(_ context.Context, runID, topicID, requestedBy string, estimatedCost, threshold float64) error {
	s.approvalArgs = struct {
		runID       string
		topicID     string
		requestedBy string
		estimated   float64
		threshold   float64
	}{runID: runID, topicID: topicID, requestedBy: requestedBy, estimated: estimatedCost, threshold: threshold}
	return s.approvalErr
}

type publisherStub struct {
	stream    string
	event     string
	payload   map[string]interface{}
	callCount int
	err       error
}

func (p *publisherStub) PublishRaw(_ context.Context, stream, eventType, version string, payload interface{}, _ ...streams.PublishOption) (string, error) {
	p.stream = stream
	p.event = eventType
	p.callCount++
	if m, ok := payload.(map[string]interface{}); ok {
		p.payload = m
	}
	return "0-0", p.err
}

type resumeStoreStub struct {
	checkpoints []store.Checkpoint
	received    store.Checkpoint
	config      budget.Config
	hasConfig   bool
	pendingRun  string
	approval    struct {
		runID       string
		topicID     string
		requestedBy string
		estimated   float64
		threshold   float64
	}
	markedStatuses []struct {
		runID  string
		stage  string
		status string
	}
	upserts []store.Checkpoint
}

func (s *resumeStoreStub) ClaimIdempotency(context.Context, string, string) (bool, error) {
	return true, nil
}

func (s *resumeStoreStub) UpsertCheckpoint(_ context.Context, cp store.Checkpoint) error {
	s.upserts = append(s.upserts, cp)
	return nil
}

func (s *resumeStoreStub) GetCheckpoint(_ context.Context, runID, stage string) (store.Checkpoint, bool, error) {
	if stage == stageRunReceived && runID == s.received.RunID {
		return s.received, true, nil
	}
	return store.Checkpoint{}, false, nil
}

func (s *resumeStoreStub) ListCheckpointsByStatus(context.Context, ...string) ([]store.Checkpoint, error) {
	return s.checkpoints, nil
}

func (s *resumeStoreStub) MarkCheckpointStatus(_ context.Context, runID, stage, status string) error {
	s.markedStatuses = append(s.markedStatuses, struct {
		runID  string
		stage  string
		status string
	}{runID: runID, stage: stage, status: status})
	return nil
}

func (s *resumeStoreStub) CreateRun(context.Context, string, string) (string, error) { return "", nil }

func (s *resumeStoreStub) GetTopicBudgetConfig(context.Context, string) (budget.Config, bool, error) {
	return s.config, s.hasConfig, nil
}

func (s *resumeStoreStub) GetPendingBudgetApproval(context.Context, string) (store.BudgetApprovalRecord, bool, error) {
	return store.BudgetApprovalRecord{}, false, nil
}

func (s *resumeStoreStub) MarkRunPendingApproval(_ context.Context, runID string) error {
	s.pendingRun = runID
	return nil
}

func (s *resumeStoreStub) CreateBudgetApproval(_ context.Context, runID, topicID, requestedBy string, estimatedCost, threshold float64) error {
	s.approval = struct {
		runID       string
		topicID     string
		requestedBy string
		estimated   float64
		threshold   float64
	}{runID: runID, topicID: topicID, requestedBy: requestedBy, estimated: estimatedCost, threshold: threshold}
	return nil
}

func TestEnforceBudgetGateCreatesApproval(t *testing.T) {
	cfg := budget.Config{RequireApproval: true}
	storeStub := &budgetStoreStub{budgetConfig: cfg, budgetExists: true}
	publisherStub := &publisherStub{}
	proc := &Processor{logger: nil, store: storeStub, publisher: publisherStub}

	payload := RunEnqueuedPayload{TopicID: "topic-1", UserID: "user-1"}
	skip, err := proc.enforceBudgetGate(context.Background(), "run-1", payload)
	if err != nil {
		t.Fatalf("enforceBudgetGate returned error: %v", err)
	}
	if !skip {
		t.Fatalf("expected gate to skip dispatch")
	}
	if storeStub.pendingRunID != "run-1" {
		t.Fatalf("expected pending run to be recorded, got %s", storeStub.pendingRunID)
	}
	if storeStub.approvalArgs.runID != "run-1" || storeStub.approvalArgs.topicID != "topic-1" {
		t.Fatalf("unexpected approval args: %+v", storeStub.approvalArgs)
	}
	if publisherStub.callCount != 1 {
		t.Fatalf("expected approval event to be published")
	}
	if publisherStub.stream != StreamBudgetApprovalRequested {
		t.Fatalf("expected event stream %s, got %s", StreamBudgetApprovalRequested, publisherStub.stream)
	}
	if ts, ok := publisherStub.payload["created_at"].(string); !ok || ts == "" {
		t.Fatalf("expected created_at in payload, got %+v", publisherStub.payload)
	}
}

func TestEnforceBudgetGateNoBudget(t *testing.T) {
	storeStub := &budgetStoreStub{}
	proc := &Processor{store: storeStub}
	payload := RunEnqueuedPayload{TopicID: "topic-1"}
	skip, err := proc.enforceBudgetGate(context.Background(), "run-1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Fatalf("expected gate not to skip dispatch")
	}
	if storeStub.pendingRunID != "" {
		t.Fatalf("did not expect pending run to be recorded")
	}
}

func TestResumePendingSkipsWhenApprovalRequired(t *testing.T) {
	dispatch := map[string]interface{}{
		"run_id": fmt.Sprintf("bootstrap:%s", "run-123"),
		"parameters": map[string]interface{}{
			"context":     map[string]interface{}{"topic_id": "topic-7"},
			"preferences": map[string]interface{}{},
		},
	}
	checkpoint := store.Checkpoint{
		RunID:     "run-123",
		Stage:     stageBootstrapTask,
		Payload:   map[string]interface{}{"payload": map[string]interface{}{"dispatch": dispatch}},
		UpdatedAt: time.Now().Add(-1 * time.Minute),
	}
	received := store.Checkpoint{
		RunID: "run-123",
		Payload: map[string]interface{}{
			"topic_id":    "topic-7",
			"user_id":     "user-9",
			"trigger":     "manual",
			"preferences": map[string]interface{}{},
			"context":     map[string]interface{}{},
		},
	}
	storeStub := &resumeStoreStub{
		checkpoints: []store.Checkpoint{checkpoint},
		received:    received,
		config:      budget.Config{RequireApproval: true},
		hasConfig:   true,
	}
	proc := &Processor{
		logger:    log.New(io.Discard, "", 0),
		store:     storeStub,
		publisher: &publisherStub{},
	}
	stats, err := proc.ResumePending(context.Background())
	if err != nil {
		t.Fatalf("ResumePending returned error: %v", err)
	}
	if stats.SkippedForApproval != 1 {
		t.Fatalf("expected skipped_for_approval=1 got %+v", stats)
	}
	if stats.Checkpoints != 0 || stats.Reclaimed != 0 {
		t.Fatalf("expected no checkpoints dispatched or reclaimed, got %+v", stats)
	}
	if storeStub.pendingRun != "run-123" {
		t.Fatalf("expected pending run to be recorded, got %s", storeStub.pendingRun)
	}
	if len(storeStub.markedStatuses) != 1 || storeStub.markedStatuses[0].status != store.CheckpointStatusCompleted {
		t.Fatalf("expected checkpoint to be marked completed, got %+v", storeStub.markedStatuses)
	}
}

func TestResumePendingRedispatchesTask(t *testing.T) {
	dispatch := map[string]interface{}{
		"run_id": fmt.Sprintf("bootstrap:%s", "run-321"),
		"parameters": map[string]interface{}{
			"context":     map[string]interface{}{"topic_id": "topic-8"},
			"preferences": map[string]interface{}{},
		},
	}
	checkpoint := store.Checkpoint{
		RunID:   "run-321",
		Stage:   stageBootstrapTask,
		Payload: map[string]interface{}{"payload": map[string]interface{}{"dispatch": dispatch}},
	}
	received := store.Checkpoint{
		RunID: "run-321",
		Payload: map[string]interface{}{
			"topic_id":    "topic-8",
			"user_id":     "user-2",
			"trigger":     "schedule",
			"preferences": map[string]interface{}{},
			"context":     map[string]interface{}{},
		},
	}
	publisher := &publisherStub{}
	storeStub := &resumeStoreStub{
		checkpoints: []store.Checkpoint{checkpoint},
		received:    received,
		config:      budget.Config{},
		hasConfig:   true,
	}
	proc := &Processor{
		logger:    log.New(io.Discard, "", 0),
		store:     storeStub,
		publisher: publisher,
	}
	stats, err := proc.ResumePending(context.Background())
	if err != nil {
		t.Fatalf("ResumePending returned error: %v", err)
	}
	if stats.Checkpoints != 1 {
		t.Fatalf("expected checkpoint to dispatch, got %+v", stats)
	}
	if publisher.callCount != 1 {
		t.Fatalf("expected publish call, got %d", publisher.callCount)
	}
	if storeStub.pendingRun != "" {
		t.Fatalf("did not expect pending approval run; got %s", storeStub.pendingRun)
	}
	if len(storeStub.upserts) != 1 || storeStub.upserts[0].Retries != 1 {
		t.Fatalf("expected checkpoint retries to increment, got %+v", storeStub.upserts)
	}
}
