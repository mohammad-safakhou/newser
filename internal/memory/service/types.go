package service

import (
	"context"
	"encoding/json"
	"time"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
)

// Manager exposes episodic memory operations used by API handlers and background jobs.
type Manager interface {
	WriteEpisode(ctx context.Context, snapshot agentcore.EpisodicSnapshot) error
	Summarize(ctx context.Context, req SummaryRequest) (SummaryResponse, error)
	Delta(ctx context.Context, req DeltaRequest) (DeltaResponse, error)
	Health(ctx context.Context) (HealthStats, error)
	ListFingerprints(ctx context.Context, topicID string, limit int) ([]agentcore.TemplateFingerprintState, error)
	PromoteFingerprint(ctx context.Context, req TemplatePromotionRequest) (agentcore.ProceduralTemplate, error)
	ListTemplates(ctx context.Context, topicID string) ([]agentcore.ProceduralTemplate, error)
	ApproveTemplate(ctx context.Context, req TemplateApprovalRequest) (agentcore.ProceduralTemplate, error)
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

// HealthStats aggregates high-level signals about episodic and semantic memory.
type HealthStats struct {
	Episodes          int64  `json:"episodes"`
	RunEmbeddings     int64  `json:"run_embeddings"`
	PlanEmbeddings    int64  `json:"plan_embeddings"`
	NovelDeltaCount   int64  `json:"novel_deltas"`
	DuplicateDeltaCnt int64  `json:"duplicate_deltas"`
	LastDeltaAt       string `json:"last_delta_at,omitempty"`
	CollectedAt       string `json:"collected_at"`
}

// Templates exposes procedural template workflows managed by memory.
type Templates interface {
	ListFingerprints(ctx context.Context, topicID string, limit int) ([]agentcore.TemplateFingerprintState, error)
	PromoteFingerprint(ctx context.Context, req TemplatePromotionRequest) (agentcore.ProceduralTemplate, error)
	ListTemplates(ctx context.Context, topicID string) ([]agentcore.ProceduralTemplate, error)
	ApproveTemplate(ctx context.Context, req TemplateApprovalRequest) (agentcore.ProceduralTemplate, error)
}

// TemplatePromotionRequest declares how a fingerprint should be promoted into a template.
type TemplatePromotionRequest struct {
	TopicID     string                 `json:"topic_id"`
	Fingerprint string                 `json:"fingerprint"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	CreatedBy   string                 `json:"created_by,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// TemplateApprovalRequest captures the details required to approve a template version.
type TemplateApprovalRequest struct {
	TemplateID string                 `json:"template_id"`
	ApprovedBy string                 `json:"approved_by"`
	Changelog  string                 `json:"changelog,omitempty"`
	Graph      json.RawMessage        `json:"graph,omitempty"`
	Parameters json.RawMessage        `json:"parameters,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}
