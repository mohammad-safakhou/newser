package streams

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// LagMetrics captures queue lag and pending state for a consumer group.
type LagMetrics struct {
	Pending    int64
	Lag        int64
	Consumers  int64
	OldestIdle time.Duration
}

// GroupLag returns lag metrics for the provided stream/group.
func GroupLag(ctx context.Context, client *redis.Client, stream, group string) (LagMetrics, error) {
	if client == nil {
		return LagMetrics{}, fmt.Errorf("redis client is nil")
	}
	if stream == "" {
		return LagMetrics{}, fmt.Errorf("stream is required")
	}
	if group == "" {
		return LagMetrics{}, fmt.Errorf("group is required")
	}

	groups, err := client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		return LagMetrics{}, fmt.Errorf("xinfo groups: %w", err)
	}
	metrics := LagMetrics{Lag: -1}
	for _, info := range groups {
		if info.Name != group {
			continue
		}
		metrics.Pending = info.Pending
		metrics.Lag = info.Lag
		metrics.Consumers = int64(info.Consumers)
		break
	}

	if metrics.Pending > 0 {
		entries, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{
			Stream: stream,
			Group:  group,
			Start:  "-",
			End:    "+",
			Count:  1,
		}).Result()
		if err != nil && err != redis.Nil {
			return LagMetrics{}, fmt.Errorf("xpendingext: %w", err)
		}
		if len(entries) > 0 {
			metrics.OldestIdle = entries[0].Idle
		}
	}

	return metrics, nil
}
