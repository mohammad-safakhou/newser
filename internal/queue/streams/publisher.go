package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Publisher wraps Redis Stream publishing with schema validation.
type Publisher struct {
	client   *redis.Client
	registry *SchemaRegistry
}

// PublishOption allows configuring Redis XADD behaviour.
type PublishOption func(*redis.XAddArgs)

// WithMaxLenApprox sets an approximate max length for the stream.
func WithMaxLenApprox(maxLen int64) PublishOption {
	return func(args *redis.XAddArgs) {
		if maxLen > 0 {
			args.MaxLen = maxLen
			args.Approx = true
		}
	}
}

// WithID overwrites the auto-generated stream ID (advanced use only).
func WithID(id string) PublishOption {
	return func(args *redis.XAddArgs) {
		if id != "" {
			args.ID = id
		}
	}
}

// NewPublisher creates a Publisher instance.
func NewPublisher(client *redis.Client, registry *SchemaRegistry) *Publisher {
	return &Publisher{client: client, registry: registry}
}

// Publish validates the envelope and appends it to the given Redis stream.
func (p *Publisher) Publish(ctx context.Context, stream string, envelope Envelope, opts ...PublishOption) (string, error) {
	if stream == "" {
		return "", fmt.Errorf("stream name is required")
	}
	if envelope.EventID == "" {
		envelope.EventID = uuid.NewString()
	}
	if envelope.OccurredAt.IsZero() {
		envelope.OccurredAt = time.Now().UTC()
	}
	if err := envelope.ValidateBasic(); err != nil {
		return "", err
	}

	if p.registry != nil {
		if err := p.registry.Validate(envelope.EventType, envelope.PayloadVersion, envelope.Data); err != nil {
			return "", err
		}
	}

	raw, err := envelope.Marshal()
	if err != nil {
		return "", err
	}

	args := &redis.XAddArgs{
		Stream: stream,
		Values: map[string]interface{}{"envelope": raw},
	}
	for _, opt := range opts {
		opt(args)
	}

	id, err := p.client.XAdd(ctx, args).Result()
	if err != nil {
		return "", fmt.Errorf("xadd: %w", err)
	}
	return id, nil
}

// PublishRaw takes an arbitrary payload and wraps it in an envelope before publishing.
func (p *Publisher) PublishRaw(ctx context.Context, stream, eventType, version string, payload interface{}, opts ...PublishOption) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	env := Envelope{
		EventType:      eventType,
		PayloadVersion: version,
		Data:           data,
	}
	return p.Publish(ctx, stream, env, opts...)
}
