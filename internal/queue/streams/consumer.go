package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Consumer reads envelopes from Redis Streams using consumer groups.
type Consumer struct {
	client   *redis.Client
	registry *SchemaRegistry
	group    string
	name     string
}

// ConsumerOption configures consumer behaviour on read.
type ConsumerOption func(*redis.XReadGroupArgs)

// WithBlock sets the maximum blocking duration when reading.
func WithBlock(d time.Duration) ConsumerOption {
	return func(args *redis.XReadGroupArgs) {
		if d > 0 {
			args.Block = d
		}
	}
}

// WithCount caps the number of messages returned in a single read.
func WithCount(n int64) ConsumerOption {
	return func(args *redis.XReadGroupArgs) {
		if n > 0 {
			args.Count = n
		}
	}
}

// WithNoAck disables automatic acknowledgement after read.
func WithNoAck() ConsumerOption {
	return func(args *redis.XReadGroupArgs) {
		args.NoAck = true
	}
}

// NewConsumer builds a new consumer for the specified group and name.
func NewConsumer(client *redis.Client, registry *SchemaRegistry, group, name string) *Consumer {
	return &Consumer{client: client, registry: registry, group: group, name: name}
}

// EnsureGroup creates the consumer group if it does not exist.
func EnsureGroup(ctx context.Context, client *redis.Client, stream, group string) error {
	if stream == "" || group == "" {
		return fmt.Errorf("stream and group must be provided")
	}
	if err := client.XGroupCreateMkStream(ctx, stream, group, "$").Err(); err != nil {
		if strings.Contains(err.Error(), "BUSYGROUP") {
			return nil
		}
		return fmt.Errorf("xgroup create: %w", err)
	}
	return nil
}

// Message represents a consumed stream entry.
type Message struct {
	ID       string
	Envelope Envelope
}

// Read pulls messages from the provided stream using the configured group/name.
func (c *Consumer) Read(ctx context.Context, stream string, opts ...ConsumerOption) ([]Message, error) {
	if stream == "" {
		return nil, fmt.Errorf("stream name is required")
	}
	if c.group == "" || c.name == "" {
		return nil, fmt.Errorf("consumer group and name must be configured")
	}

	args := &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.name,
		Streams:  []string{stream, ">"},
	}
	for _, opt := range opts {
		opt(args)
	}

	streams, err := c.client.XReadGroup(ctx, args).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	var out []Message
	for _, st := range streams {
		for _, msg := range st.Messages {
			raw, ok := msg.Values["envelope"]
			if !ok {
				_ = c.client.XAck(ctx, stream, c.group, msg.ID).Err()
				continue
			}

			var bytesData []byte
			switch v := raw.(type) {
			case string:
				bytesData = []byte(v)
			case []byte:
				bytesData = v
			default:
				data, marshalErr := json.Marshal(v)
				if marshalErr != nil {
					_ = c.client.XAck(ctx, stream, c.group, msg.ID).Err()
					continue
				}
				bytesData = data
			}

			env, envErr := UnmarshalEnvelope(bytesData)
			if envErr != nil {
				_ = c.client.XAck(ctx, stream, c.group, msg.ID).Err()
				continue
			}
			if c.registry != nil {
				if err := c.registry.Validate(env.EventType, env.PayloadVersion, env.Data); err != nil {
					_ = c.client.XAck(ctx, stream, c.group, msg.ID).Err()
					continue
				}
			}
			out = append(out, Message{ID: msg.ID, Envelope: env})
		}
	}
	return out, nil
}

// Ack acknowledges processing of the provided message IDs.
func (c *Consumer) Ack(ctx context.Context, stream string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := c.client.XAck(ctx, stream, c.group, ids...).Err(); err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	return nil
}
