package streams

import (
	"encoding/json"
	"fmt"
	"time"
)

// Envelope represents the canonical message wrapper persisted to Redis Streams.
type Envelope struct {
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	OccurredAt     time.Time       `json:"occurred_at"`
	TraceID        string          `json:"trace_id,omitempty"`
	Attempt        int             `json:"attempt"`
	PayloadVersion string          `json:"payload_version"`
	Data           json.RawMessage `json:"data"`
}

// ValidateBasic ensures mandatory envelope fields are present before schema validation.
func (e *Envelope) ValidateBasic() error {
	if e.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	if e.EventType == "" {
		return fmt.Errorf("event_type is required")
	}
	if e.PayloadVersion == "" {
		return fmt.Errorf("payload_version is required")
	}
	if e.Attempt < 0 {
		return fmt.Errorf("attempt must be >= 0")
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("data payload is required")
	}
	return nil
}

// Marshal returns the JSON encoding of the envelope.
func (e *Envelope) Marshal() ([]byte, error) {
	if err := e.ValidateBasic(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// UnmarshalEnvelope parses JSON bytes into an Envelope and validates required fields.
func UnmarshalEnvelope(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return env, fmt.Errorf("unmarshal envelope: %w", err)
	}
	if err := env.ValidateBasic(); err != nil {
		return env, err
	}
	return env, nil
}
