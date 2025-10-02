package worker

import (
	"context"
	"fmt"

	"github.com/mohammad-safakhou/newser/internal/executor"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type taskDispatchRunner struct {
	publisher  *streams.Publisher
	taskStream string
	counter    otelmetric.Int64Counter
}

func (r *taskDispatchRunner) RunTask(ctx context.Context, runID string, task executor.Task) error {
	if r.publisher == nil {
		return fmt.Errorf("publisher not configured")
	}
	payload, ok := task.Payload["dispatch"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing dispatch payload for task %s", task.ID)
	}
	if _, err := r.publisher.PublishRaw(ctx, r.taskStream, StreamTaskDispatch, "v1", payload); err != nil {
		return err
	}
	if r.counter != nil {
		r.counter.Add(ctx, 1)
	}
	return nil
}

var _ executor.TaskRunner = (*taskDispatchRunner)(nil)
