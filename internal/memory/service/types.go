package service

import (
	"context"
	"time"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
)

// Manager exposes episodic memory operations used by API handlers and background jobs.
type Manager interface {
	WriteEpisode(ctx context.Context, snapshot agentcore.EpisodicSnapshot) error
	Summarize(ctx context.Context, req SummaryRequest) (SummaryResponse, error)
	Delta(ctx context.Context, req DeltaRequest) (DeltaResponse, error)
}

// SummaryRequest declares which runs should participate in a topic summary.
type SummaryRequest struct {
	TopicID string `json:"topic_id"`
	MaxRuns int    `json:"max_runs,omitempty"`
}

// SummaryItem captures a single run contribution within a summary response.
type SummaryItem struct {
	RunID      string     `json:"run_id"`
	Summary    string     `json:"summary,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// SummaryResponse aggregates recent run summaries for a topic.
type SummaryResponse struct {
	TopicID     string        `json:"topic_id"`
	GeneratedAt time.Time     `json:"generated_at"`
	Items       []SummaryItem `json:"items"`
	Summary     string        `json:"summary"`
}

// DeltaItem represents a candidate memory item used for deduplication.
type DeltaItem struct {
	ID       string                 `json:"id"`
	Hash     string                 `json:"hash,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// DeltaRequest provides the context for memory delta calculations.
type DeltaRequest struct {
	TopicID  string      `json:"topic_id"`
	Items    []DeltaItem `json:"items"`
	KnownIDs []string    `json:"known_ids,omitempty"`
}

// DeltaResponse summarises the deduplicated results of a delta calculation.
type DeltaResponse struct {
	TopicID        string      `json:"topic_id"`
	Novel          []DeltaItem `json:"novel"`
	DuplicateCount int         `json:"duplicate_count"`
	Total          int         `json:"total"`
}
