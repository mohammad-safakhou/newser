package executor

import (
	"context"

	"github.com/mohammad-safakhou/newser/internal/store"
)

// StoreCheckpointManager persists checkpoints using the shared store.
type checkpointStore interface {
	UpsertCheckpoint(ctx context.Context, cp store.Checkpoint) error
	MarkCheckpointStatus(ctx context.Context, runID, stage, status string) error
}

type StoreCheckpointManager struct {
	store checkpointStore
}

// NewStoreCheckpointManager constructs a CheckpointManager backed by store.Store.
func NewStoreCheckpointManager(st checkpointStore) *StoreCheckpointManager {
	return &StoreCheckpointManager{store: st}
}

func (m *StoreCheckpointManager) StartRun(ctx context.Context, runID string) error {
	// no-op: we track work at task granularity
	return nil
}

func (m *StoreCheckpointManager) SaveTaskStart(ctx context.Context, runID string, task Task, attempt int) error {
	if m.store == nil {
		return nil
	}
	stage := task.Stage
	if stage == "" {
		stage = task.ID
	}
	token := task.CheckpointToken
	if token == "" {
		token = task.ID
	}
	cp := store.Checkpoint{
		RunID:           runID,
		Stage:           stage,
		Status:          store.CheckpointStatusDispatched,
		CheckpointToken: token,
		Payload: map[string]interface{}{
			"task_id": task.ID,
			"payload": task.Payload,
		},
		Retries: attempt,
	}
	return m.store.UpsertCheckpoint(ctx, cp)
}

func (m *StoreCheckpointManager) SaveTaskSuccess(ctx context.Context, runID string, task Task, attempt int) error {
	return nil
}

func (m *StoreCheckpointManager) SaveTaskFailure(ctx context.Context, runID string, task Task, attempt int, err error) error {
	if m.store == nil {
		return nil
	}
	stage := task.Stage
	if stage == "" {
		stage = task.ID
	}
	token := task.CheckpointToken
	if token == "" {
		token = task.ID
	}
	payload := map[string]interface{}{"task_id": task.ID}
	if len(task.Payload) > 0 {
		payload["payload"] = task.Payload
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	cp := store.Checkpoint{
		RunID:           runID,
		Stage:           stage,
		Status:          store.CheckpointStatusCompleted,
		CheckpointToken: token,
		Payload:         payload,
		Retries:         attempt,
	}
	return m.store.UpsertCheckpoint(ctx, cp)
}

var _ CheckpointManager = (*StoreCheckpointManager)(nil)
