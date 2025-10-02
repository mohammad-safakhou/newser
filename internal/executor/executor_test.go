package executor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type stubCheckpoint struct {
	startErr error
	events   []string
}

func (s *stubCheckpoint) StartRun(ctx context.Context, runID string) error {
	if s.startErr != nil {
		return s.startErr
	}
	s.events = append(s.events, "start:"+runID)
	return nil
}

func (s *stubCheckpoint) SaveTaskStart(ctx context.Context, runID string, task Task, attempt int) error {
	s.events = append(s.events, fmt.Sprintf("task_start:%s:%d", task.ID, attempt))
	return nil
}

func (s *stubCheckpoint) SaveTaskSuccess(ctx context.Context, runID string, task Task, attempt int) error {
	s.events = append(s.events, fmt.Sprintf("task_success:%s:%d", task.ID, attempt))
	return nil
}

func (s *stubCheckpoint) SaveTaskFailure(ctx context.Context, runID string, task Task, attempt int, err error) error {
	s.events = append(s.events, fmt.Sprintf("task_failure:%s:%d", task.ID, attempt))
	return nil
}

type stubRunner struct {
	calls  []string
	err    error
	errors []error
	index  int
}

func (s *stubRunner) RunTask(ctx context.Context, runID string, task Task) error {
	s.calls = append(s.calls, task.ID)
	if s.index < len(s.errors) {
		err := s.errors[s.index]
		s.index++
		return err
	}
	return s.err
}

func TestExecuteRespectsDependencies(t *testing.T) {
	exec := New()
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1"},
		"t2": {ID: "t2", DependsOn: []string{"t1"}},
		"t3": {ID: "t3", DependsOn: []string{"t2"}},
	}}

	order, err := exec.Execute(context.Background(), "run", graph, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(order))
	}
	idx := make(map[string]int)
	for i, id := range order {
		idx[id] = i
	}
	if !(idx["t1"] < idx["t2"] && idx["t2"] < idx["t3"]) {
		t.Fatalf("dependency order incorrect: %v", order)
	}
}

func TestExecuteDetectsUnknownDependency(t *testing.T) {
	exec := New()
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1", DependsOn: []string{"missing"}},
	}}

	if _, err := exec.Execute(context.Background(), "run", graph, nil); err == nil || !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf("expected unknown dependency error, got %v", err)
	}
}

func TestExecuteDetectsCycle(t *testing.T) {
	exec := New()
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1", DependsOn: []string{"t2"}},
		"t2": {ID: "t2", DependsOn: []string{"t1"}},
	}}

	if _, err := exec.Execute(context.Background(), "run", graph, nil); err != ErrCycleDetected {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestExecutorOptionSetsCheckpointManager(t *testing.T) {
	stub := &stubCheckpoint{}
	exec := New(WithCheckpointManager(stub))
	if exec.checkpoints != stub {
		t.Fatalf("expected checkpoint manager to be set")
	}
}

func TestExecutorInvokesRunnerAndCheckpoints(t *testing.T) {
	chk := &stubCheckpoint{}
	run := &stubRunner{}
	exec := New(WithCheckpointManager(chk))
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1"},
	}}

	order, err := exec.Execute(context.Background(), "run-1", graph, run)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("unexpected order: %v", order)
	}
	if len(run.calls) != 1 || run.calls[0] != "t1" {
		t.Fatalf("runner not invoked correctly: %v", run.calls)
	}
	if len(chk.events) != 3 {
		t.Fatalf("expected checkpoint events, got %v", chk.events)
	}
	if chk.events[1] != "task_start:t1:0" || chk.events[2] != "task_success:t1:0" {
		t.Fatalf("unexpected checkpoint events: %v", chk.events)
	}
}

func TestExecutorReturnsRunnerError(t *testing.T) {
	chk := &stubCheckpoint{}
	run := &stubRunner{err: errors.New("boom")}
	exec := New(WithCheckpointManager(chk))
	graph := Graph{Tasks: map[string]Task{"t1": {ID: "t1"}}}

	if _, err := exec.Execute(context.Background(), "run-1", graph, run); err == nil {
		t.Fatalf("expected runner error")
	}
	if len(chk.events) < 3 || chk.events[1] != "task_start:t1:0" || chk.events[2] != "task_failure:t1:1" {
		t.Fatalf("expected failure events: %v", chk.events)
	}
}

var _ CheckpointManager = (*stubCheckpoint)(nil)

func TestStoreCheckpointManager(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	st := &store.Store{DB: db}
	mgr := NewStoreCheckpointManager(st)
	ctx := context.Background()
	task := Task{ID: "task", Payload: map[string]interface{}{"foo": "bar"}}

	mock.ExpectExec("INSERT INTO queue_checkpoints").
		WithArgs("run", "task", "task", store.CheckpointStatusDispatched, sqlmock.AnyArg(), 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := mgr.SaveTaskStart(ctx, "run", task, 0); err != nil {
		t.Fatalf("SaveTaskStart: %v", err)
	}

	if err := mgr.SaveTaskSuccess(ctx, "run", task, 0); err != nil {
		t.Fatalf("SaveTaskSuccess: %v", err)
	}

	mock.ExpectExec("INSERT INTO queue_checkpoints").
		WithArgs("run", "task", "task", store.CheckpointStatusCompleted, sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := mgr.SaveTaskFailure(ctx, "run", task, 1, errors.New("boom")); err != nil {
		t.Fatalf("SaveTaskFailure: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestExecutorRetriesUntilSuccess(t *testing.T) {
	chk := &stubCheckpoint{}
	run := &stubRunner{errors: []error{errors.New("boom"), nil}}
	exec := New(WithCheckpointManager(chk))
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1", MaxRetries: 2},
	}}

	order, err := exec.Execute(context.Background(), "run", graph, run)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(order) != 1 {
		t.Fatalf("expected single task, got %v", order)
	}
	if len(run.calls) != 2 {
		t.Fatalf("expected runner to retry, calls=%v", run.calls)
	}
	expected := []string{"start:run", "task_start:t1:0", "task_failure:t1:1", "task_start:t1:1", "task_success:t1:1"}
	if fmt.Sprint(chk.events) != fmt.Sprint(expected) {
		t.Fatalf("unexpected checkpoint events: %v", chk.events)
	}
}

func TestExecutorRetriesExhausted(t *testing.T) {
	chk := &stubCheckpoint{}
	run := &stubRunner{errors: []error{errors.New("boom"), errors.New("boom")}}
	exec := New(WithCheckpointManager(chk))
	graph := Graph{Tasks: map[string]Task{
		"t1": {ID: "t1", MaxRetries: 1},
	}}

	if _, err := exec.Execute(context.Background(), "run", graph, run); err == nil {
		t.Fatalf("expected error after retries exhausted")
	}
	if len(run.calls) != 2 {
		t.Fatalf("expected two attempts, got %d", len(run.calls))
	}
	expected := []string{"start:run", "task_start:t1:0", "task_failure:t1:1", "task_start:t1:1", "task_failure:t1:2"}
	if fmt.Sprint(chk.events) != fmt.Sprint(expected) {
		t.Fatalf("unexpected checkpoint events: %v", chk.events)
	}
}
