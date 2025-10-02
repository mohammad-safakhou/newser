package executor

import "context"

// CheckpointManager persists executor progress to support resume semantics.
type CheckpointManager interface {
	StartRun(ctx context.Context, runID string) error
	SaveTaskStart(ctx context.Context, runID string, task Task, attempt int) error
	SaveTaskSuccess(ctx context.Context, runID string, task Task, attempt int) error
	SaveTaskFailure(ctx context.Context, runID string, task Task, attempt int, err error) error
}

// NoopCheckpointManager is a default implementation that records nothing.
type NoopCheckpointManager struct{}

// NewNoopCheckpointManager returns a checkpoint manager that does nothing.
func NewNoopCheckpointManager() *NoopCheckpointManager { return &NoopCheckpointManager{} }

func (NoopCheckpointManager) StartRun(ctx context.Context, runID string) error { return nil }
func (NoopCheckpointManager) SaveTaskStart(ctx context.Context, runID string, task Task, attempt int) error {
	return nil
}
func (NoopCheckpointManager) SaveTaskSuccess(ctx context.Context, runID string, task Task, attempt int) error {
	return nil
}
func (NoopCheckpointManager) SaveTaskFailure(ctx context.Context, runID string, task Task, attempt int, err error) error {
	return nil
}
