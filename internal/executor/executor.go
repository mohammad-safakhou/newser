package executor

import (
	"context"
	"fmt"
	"time"
)

// Task represents a node in the execution DAG.
type Task struct {
	ID              string
	Stage           string
	DependsOn       []string
	Payload         map[string]interface{}
	CheckpointToken string
	MaxRetries      int
	RetryDelay      time.Duration
}

// Graph encapsulates a set of tasks keyed by ID.
type Graph struct {
	Tasks map[string]Task
}

// Executor is responsible for running tasks in dependency order.
type Executor struct {
	checkpoints CheckpointManager
	metrics     Metrics
}

// Metrics aggregates optional telemetry callbacks.
type Metrics struct {
	RetryCounter func(context.Context, Task, int)
	Duration     func(context.Context, Task, time.Duration)
}

// Option configures executor behaviour.
type Option func(*Executor)

// WithCheckpointManager sets the checkpoint manager implementation.
func WithCheckpointManager(mgr CheckpointManager) Option {
	return func(ex *Executor) {
		ex.checkpoints = mgr
	}
}

// WithMetrics sets executor metrics callbacks.
func WithMetrics(m Metrics) Option {
	return func(ex *Executor) {
		ex.metrics = m
	}
}

// New creates a new Executor instance.
func New(opts ...Option) *Executor {
	ex := &Executor{}
	for _, opt := range opts {
		opt(ex)
	}
	return ex
}

// ErrUnknownDependency indicates a dependency reference that is missing from the graph.
var ErrUnknownDependency = fmt.Errorf("unknown dependency")

// ErrCycleDetected indicates the graph contains a cycle.
var ErrCycleDetected = fmt.Errorf("cycle detected")

// TaskRunner executes the concrete work for a task.
type TaskRunner interface {
	RunTask(ctx context.Context, runID string, task Task) error
}

// Execute walks the supplied graph and returns an ordered list of task IDs.
func (e *Executor) Execute(ctx context.Context, runID string, g Graph, runner TaskRunner) ([]string, error) {
	order := make([]string, 0, len(g.Tasks))
	indegree := make(map[string]int, len(g.Tasks))
	adjacency := make(map[string][]string, len(g.Tasks))

	for id, task := range g.Tasks {
		if _, ok := indegree[id]; !ok {
			indegree[id] = 0
		}
		for _, dep := range task.DependsOn {
			if _, ok := g.Tasks[dep]; !ok {
				return nil, fmt.Errorf("%w: %s -> %s", ErrUnknownDependency, id, dep)
			}
			adjacency[dep] = append(adjacency[dep], id)
			indegree[id] = indegree[id] + 1
		}
	}

	if e.checkpoints == nil {
		e.checkpoints = NewNoopCheckpointManager()
	}
	if err := e.checkpoints.StartRun(ctx, runID); err != nil {
		return nil, err
	}
	queue := make([]string, 0, len(g.Tasks))
	for id := range g.Tasks {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		task := g.Tasks[current]
		if task.Stage == "" {
			task.Stage = task.ID
		}
		if task.CheckpointToken == "" {
			task.CheckpointToken = task.ID
		}
		order = append(order, current)

		maxRetries := task.MaxRetries
		if maxRetries < 0 {
			maxRetries = 0
		}
		delay := task.RetryDelay
		attempt := 0
		for {
			attemptStart := time.Now()
			if err := e.checkpoints.SaveTaskStart(ctx, runID, task, attempt); err != nil {
				return nil, err
			}
			var runErr error
			if runner != nil {
				runErr = runner.RunTask(ctx, runID, task)
			}
			if runErr == nil {
				if err := e.checkpoints.SaveTaskSuccess(ctx, runID, task, attempt); err != nil {
					return nil, err
				}
				if e.metrics.Duration != nil {
					e.metrics.Duration(ctx, task, time.Since(attemptStart))
				}
				break
			}
			nextAttempt := attempt + 1
			if err := e.checkpoints.SaveTaskFailure(ctx, runID, task, nextAttempt, runErr); err != nil {
				return nil, err
			}
			if e.metrics.RetryCounter != nil {
				e.metrics.RetryCounter(ctx, task, nextAttempt)
			}
			if nextAttempt > maxRetries {
				return nil, runErr
			}
			attempt = nextAttempt
			if delay > 0 {
				time.Sleep(delay)
			}
		}

		for _, next := range adjacency[current] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) != len(g.Tasks) {
		return nil, ErrCycleDetected
	}

	return order, nil
}
