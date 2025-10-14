package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/policy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type Store struct {
	DB *sql.DB
}

// Checkpoint statuses persisted for queue processing.
const (
	CheckpointStatusReceived   = "received"
	CheckpointStatusDispatched = "dispatched"
	CheckpointStatusCompleted  = "completed"
)

// DefaultEmbeddingDimensions indicates the expected length of semantic vectors stored in pgvector columns.
const DefaultEmbeddingDimensions = 1536

const (
	// Procedural template version statuses.
	ProceduralTemplateStatusDraft           = "draft"
	ProceduralTemplateStatusPendingApproval = "pending_approval"
	ProceduralTemplateStatusApproved        = "approved"
	ProceduralTemplateStatusDeprecated      = "deprecated"

	// Memory job types.
	MemoryJobTypeSummarise = "summarise"
	MemoryJobTypePrune     = "prune"

	// Memory job statuses.
	MemoryJobStatusPending = "pending"
	MemoryJobStatusRunning = "running"
	MemoryJobStatusSuccess = "success"
	MemoryJobStatusFailed  = "failed"
)

// Checkpoint captures durable progress for a run/task stage.
type Checkpoint struct {
	RunID           string
	Stage           string
	Status          string
	CheckpointToken string
	Payload         map[string]interface{}
	Retries         int
	UpdatedAt       time.Time
}

// SchemaRecord represents a stored message schema version.
type SchemaRecord struct {
	EventType string
	Version   string
	Schema    []byte
	Checksum  string
	CreatedAt time.Time
}

// ToolCardRecord represents a stored capability ToolCard.
type ToolCardRecord struct {
	Name         string
	Version      string
	Description  string
	AgentType    string
	InputSchema  []byte
	OutputSchema []byte
	CostEstimate float64
	SideEffects  []byte
	Checksum     string
	Signature    string
	CreatedAt    time.Time
}

// PlanGraphRecord captures a stored planner graph document.
type PlanGraphRecord struct {
	PlanID         string
	ThoughtID      string
	Version        string
	Confidence     float64
	ExecutionOrder []string
	Budget         []byte
	Estimates      []byte
	PlanJSON       []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RunEmbeddingRecord represents a stored semantic vector for a run-level artifact.
type RunEmbeddingRecord struct {
	ID        string
	RunID     string
	TopicID   string
	Kind      string
	Vector    []float32
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// PlanStepEmbeddingRecord stores semantic vectors for fine-grained plan nodes or sub-plans.
type PlanStepEmbeddingRecord struct {
	ID        string
	RunID     string
	TopicID   string
	TaskID    string
	Kind      string
	Vector    []float32
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// RunEmbeddingSearchResult represents a semantic search hit for a run-level embedding.
type RunEmbeddingSearchResult struct {
	RunID     string
	TopicID   string
	Kind      string
	Distance  float64
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// PlanStepEmbeddingSearchResult represents a semantic search hit for a plan-step embedding.
type PlanStepEmbeddingSearchResult struct {
	RunID     string
	TopicID   string
	TaskID    string
	Kind      string
	Distance  float64
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// MemoryDeltaRecord captures a summary of a delta operation for auditing.
type MemoryDeltaRecord struct {
	TopicID        string
	TotalItems     int
	NovelItems     int
	DuplicateItems int
	Semantic       bool
	Metadata       map[string]interface{}
	CreatedAt      time.Time
}

// EvidenceRecord captures statement-to-source linkage for a run.
type EvidenceRecord struct {
	RunID     string
	ClaimID   string
	Statement string
	SourceIDs []string
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// ProceduralTemplateRecord captures high-level metadata for a procedural template.
type ProceduralTemplateRecord struct {
	ID             string
	TopicID        string
	Name           string
	Description    string
	CurrentVersion int
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProceduralTemplateVersionRecord stores a concrete, versioned template definition.
type ProceduralTemplateVersionRecord struct {
	ID         int64
	TemplateID string
	Version    int
	Status     string
	Graph      json.RawMessage
	Parameters json.RawMessage
	Metadata   json.RawMessage
	Changelog  string
	ApprovedBy string
	ApprovedAt *time.Time
	CreatedAt  time.Time
}

// MemoryJobRecord represents a scheduled summarisation/pruning job entry.
type MemoryJobRecord struct {
	ID           int64
	TopicID      string
	JobType      string
	Status       string
	ScheduledFor time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	Attempts     int
	LastError    string
	Params       json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// MemoryJobResultRecord stores metrics/outcome for a completed memory job.
type MemoryJobResultRecord struct {
	JobID     int64
	Metrics   json.RawMessage
	Summary   string
	ErrorMsg  string
	CreatedAt time.Time
}

// AttachmentRecord captures metadata about artifacts persisted to attachment storage.
type AttachmentRecord struct {
	ID                 string
	RunID              string
	TaskID             string
	Attempt            int
	ArtifactID         string
	Name               string
	MediaType          string
	SizeBytes          int64
	Checksum           string
	StorageURI         string
	RetentionExpiresAt *time.Time
	Metadata           json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateProceduralTemplate inserts a new procedural template shell.
func (s *Store) CreateProceduralTemplate(ctx context.Context, rec ProceduralTemplateRecord) (ProceduralTemplateRecord, error) {
	if strings.TrimSpace(rec.Name) == "" {
		return ProceduralTemplateRecord{}, fmt.Errorf("template name required")
	}
	desc := strings.TrimSpace(rec.Description)
	topic := strings.TrimSpace(rec.TopicID)
	createdBy := strings.TrimSpace(rec.CreatedBy)

	row := s.DB.QueryRowContext(ctx, `
INSERT INTO procedural_templates (topic_id, name, description, created_by)
VALUES ($1,$2,$3,$4)
RETURNING id, topic_id, description, current_version, created_by, created_at, updated_at
`, nullableString(topic), rec.Name, nullableString(desc), nullableString(createdBy))

	var topicID sql.NullString
	var description sql.NullString
	var creator sql.NullString
	if err := row.Scan(&rec.ID, &topicID, &description, &rec.CurrentVersion, &creator, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return ProceduralTemplateRecord{}, err
	}
	if topicID.Valid {
		rec.TopicID = topicID.String
	}
	rec.Name = rec.Name
	if description.Valid {
		rec.Description = description.String
	}
	if creator.Valid {
		rec.CreatedBy = creator.String
	}
	return rec, nil
}

// GetProceduralTemplate fetches a template by identifier.
func (s *Store) GetProceduralTemplate(ctx context.Context, id string) (ProceduralTemplateRecord, bool, error) {
	if strings.TrimSpace(id) == "" {
		return ProceduralTemplateRecord{}, false, fmt.Errorf("template id required")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT id, topic_id, name, description, current_version, created_by, created_at, updated_at
FROM procedural_templates
WHERE id=$1
`, id)
	var rec ProceduralTemplateRecord
	var topicID sql.NullString
	var description sql.NullString
	var creator sql.NullString
	if err := row.Scan(&rec.ID, &topicID, &rec.Name, &description, &rec.CurrentVersion, &creator, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProceduralTemplateRecord{}, false, nil
		}
		return ProceduralTemplateRecord{}, false, err
	}
	if topicID.Valid {
		rec.TopicID = topicID.String
	}
	if description.Valid {
		rec.Description = description.String
	}
	if creator.Valid {
		rec.CreatedBy = creator.String
	}
	return rec, true, nil
}

// ListProceduralTemplates returns templates optionally filtered by topic.
func (s *Store) ListProceduralTemplates(ctx context.Context, topicID string) ([]ProceduralTemplateRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(topicID) == "" {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, topic_id, name, description, current_version, created_by, created_at, updated_at
FROM procedural_templates
ORDER BY created_at DESC
`)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, topic_id, name, description, current_version, created_by, created_at, updated_at
FROM procedural_templates
WHERE topic_id=$1
ORDER BY created_at DESC
`, topicID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProceduralTemplateRecord
	for rows.Next() {
		var rec ProceduralTemplateRecord
		var topic sql.NullString
		var description sql.NullString
		var creator sql.NullString
		if err := rows.Scan(&rec.ID, &topic, &rec.Name, &description, &rec.CurrentVersion, &creator, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		if topic.Valid {
			rec.TopicID = topic.String
		}
		if description.Valid {
			rec.Description = description.String
		}
		if creator.Valid {
			rec.CreatedBy = creator.String
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CreateProceduralTemplateVersion inserts a new template version and optionally updates the active version.
func (s *Store) CreateProceduralTemplateVersion(ctx context.Context, rec ProceduralTemplateVersionRecord) (ProceduralTemplateVersionRecord, error) {
	if strings.TrimSpace(rec.TemplateID) == "" {
		return ProceduralTemplateVersionRecord{}, fmt.Errorf("template_id required")
	}
	if len(rec.Graph) == 0 {
		return ProceduralTemplateVersionRecord{}, fmt.Errorf("graph payload required")
	}
	params := defaultJSON(rec.Parameters)
	meta := defaultJSON(rec.Metadata)
	status := rec.Status
	if status == "" {
		status = ProceduralTemplateStatusDraft
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ProceduralTemplateVersionRecord{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	version := rec.Version
	if version <= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM procedural_template_versions WHERE template_id=$1`, rec.TemplateID).Scan(&version); err != nil {
			return ProceduralTemplateVersionRecord{}, err
		}
	}

	var approvedBy sql.NullString
	if strings.TrimSpace(rec.ApprovedBy) != "" {
		approvedBy = sql.NullString{String: strings.TrimSpace(rec.ApprovedBy), Valid: true}
	}
	var approvedAt sql.NullTime
	if rec.ApprovedAt != nil && !rec.ApprovedAt.IsZero() {
		approvedAt = sql.NullTime{Time: rec.ApprovedAt.UTC(), Valid: true}
	}

	insert := tx.QueryRowContext(ctx, `
INSERT INTO procedural_template_versions (template_id, version, status, graph, parameters, metadata, changelog, approved_by, approved_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING id, status, graph, parameters, metadata, changelog, approved_by, approved_at, created_at
`, rec.TemplateID, version, status, rec.Graph, params, meta, nullableString(strings.TrimSpace(rec.Changelog)), approvedBy, approvedAt)

	var dbApprovedBy sql.NullString
	var dbApprovedAt sql.NullTime
	if err := insert.Scan(&rec.ID, &rec.Status, &rec.Graph, &rec.Parameters, &rec.Metadata, &rec.Changelog, &dbApprovedBy, &dbApprovedAt, &rec.CreatedAt); err != nil {
		return ProceduralTemplateVersionRecord{}, err
	}
	rec.Version = version
	if dbApprovedBy.Valid {
		rec.ApprovedBy = dbApprovedBy.String
	}
	if dbApprovedAt.Valid {
		ts := dbApprovedAt.Time
		rec.ApprovedAt = &ts
	}

	updateQuery := `UPDATE procedural_templates SET updated_at = NOW() WHERE id=$1`
	if _, err := tx.ExecContext(ctx, updateQuery, rec.TemplateID); err != nil {
		return ProceduralTemplateVersionRecord{}, err
	}

	if rec.Status == ProceduralTemplateStatusApproved {
		if _, err := tx.ExecContext(ctx, `UPDATE procedural_templates SET current_version=$1, updated_at=NOW() WHERE id=$2`, rec.Version, rec.TemplateID); err != nil {
			return ProceduralTemplateVersionRecord{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return ProceduralTemplateVersionRecord{}, err
	}
	return rec, nil
}

// ListProceduralTemplateVersions returns all versions for the specified template ordered by newest first.
func (s *Store) ListProceduralTemplateVersions(ctx context.Context, templateID string) ([]ProceduralTemplateVersionRecord, error) {
	if strings.TrimSpace(templateID) == "" {
		return nil, fmt.Errorf("template_id required")
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, template_id, version, status, graph, parameters, metadata, changelog, approved_by, approved_at, created_at
FROM procedural_template_versions
WHERE template_id=$1
ORDER BY version DESC
`, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProceduralTemplateVersionRecord
	for rows.Next() {
		var rec ProceduralTemplateVersionRecord
		var approvedBy sql.NullString
		var approvedAt sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.TemplateID, &rec.Version, &rec.Status, &rec.Graph, &rec.Parameters, &rec.Metadata, &rec.Changelog, &approvedBy, &approvedAt, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if approvedBy.Valid {
			rec.ApprovedBy = approvedBy.String
		}
		if approvedAt.Valid {
			ts := approvedAt.Time
			rec.ApprovedAt = &ts
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CreateMemoryJob enqueues a summarisation or pruning job.
func (s *Store) CreateMemoryJob(ctx context.Context, rec MemoryJobRecord) (MemoryJobRecord, error) {
	if strings.TrimSpace(rec.TopicID) == "" {
		return MemoryJobRecord{}, fmt.Errorf("topic_id required")
	}
	if rec.JobType != MemoryJobTypeSummarise && rec.JobType != MemoryJobTypePrune {
		return MemoryJobRecord{}, fmt.Errorf("invalid job_type: %s", rec.JobType)
	}
	status := rec.Status
	if status == "" {
		status = MemoryJobStatusPending
	}
	scheduled := rec.ScheduledFor
	if scheduled.IsZero() {
		scheduled = time.Now().UTC()
	}
	var started interface{}
	if rec.StartedAt != nil && !rec.StartedAt.IsZero() {
		t := rec.StartedAt.UTC()
		started = t
	}
	params := defaultJSON(rec.Params)

	row := s.DB.QueryRowContext(ctx, `
INSERT INTO memory_jobs (topic_id, job_type, status, scheduled_for, started_at, params)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, status, scheduled_for, started_at, completed_at, attempts, COALESCE(last_error,''), params, created_at, updated_at
`, rec.TopicID, rec.JobType, status, scheduled, started, params)

	var startedAt sql.NullTime
	var completedAt sql.NullTime
	if err := row.Scan(&rec.ID, &rec.Status, &rec.ScheduledFor, &startedAt, &completedAt, &rec.Attempts, &rec.LastError, &rec.Params, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return MemoryJobRecord{}, err
	}
	if startedAt.Valid {
		ts := startedAt.Time
		rec.StartedAt = &ts
	}
	if completedAt.Valid {
		ts := completedAt.Time
		rec.CompletedAt = &ts
	}
	return rec, nil
}

// CompleteMemoryJob updates status/metrics for a finished memory job.
func (s *Store) CompleteMemoryJob(ctx context.Context, jobID int64, status string, result MemoryJobResultRecord) error {
	if jobID == 0 {
		return fmt.Errorf("job_id required")
	}
	if status != MemoryJobStatusSuccess && status != MemoryJobStatusFailed {
		return fmt.Errorf("invalid status: %s", status)
	}
	metrics := defaultJSON(result.Metrics)
	summary := strings.TrimSpace(result.Summary)
	lastError := strings.TrimSpace(result.ErrorMsg)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	completedAt := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `
UPDATE memory_jobs
SET status=$1,
    completed_at=$2,
    updated_at=NOW(),
    last_error=$3
WHERE id=$4
`, status, completedAt, nullableString(lastError), jobID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("memory job %d not found", jobID)
	}

	if len(metrics) > 0 || summary != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_job_results (job_id, metrics, summary)
VALUES ($1,$2,$3)
`, jobID, metrics, nullableString(summary)); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ListMemoryJobs retrieves jobs filtered by topic and/or type.
func (s *Store) ListMemoryJobs(ctx context.Context, topicID, jobType string, limit int) ([]MemoryJobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if strings.TrimSpace(topicID) == "" && strings.TrimSpace(jobType) == "" {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, topic_id, job_type, status, scheduled_for, started_at, completed_at, attempts, COALESCE(last_error,''), params, created_at, updated_at
FROM memory_jobs
ORDER BY created_at DESC
LIMIT $1
`, limit)
	} else if strings.TrimSpace(jobType) == "" {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, topic_id, job_type, status, scheduled_for, started_at, completed_at, attempts, COALESCE(last_error,''), params, created_at, updated_at
FROM memory_jobs
WHERE topic_id=$1
ORDER BY created_at DESC
LIMIT $2
`, topicID, limit)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, topic_id, job_type, status, scheduled_for, started_at, completed_at, attempts, COALESCE(last_error,''), params, created_at, updated_at
FROM memory_jobs
WHERE topic_id=$1 AND job_type=$2
ORDER BY created_at DESC
LIMIT $3
`, topicID, jobType, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryJobRecord
	for rows.Next() {
		var rec MemoryJobRecord
		var started sql.NullTime
		var completed sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.TopicID, &rec.JobType, &rec.Status, &rec.ScheduledFor, &started, &completed, &rec.Attempts, &rec.LastError, &rec.Params, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		if started.Valid {
			ts := started.Time
			rec.StartedAt = &ts
		}
		if completed.Valid {
			ts := completed.Time
			rec.CompletedAt = &ts
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// InsertAttachment stores metadata about an execution artifact.
func (s *Store) InsertAttachment(ctx context.Context, rec AttachmentRecord) (AttachmentRecord, error) {
	if strings.TrimSpace(rec.RunID) == "" {
		return AttachmentRecord{}, fmt.Errorf("run_id required")
	}
	if strings.TrimSpace(rec.TaskID) == "" {
		return AttachmentRecord{}, fmt.Errorf("task_id required")
	}
	if strings.TrimSpace(rec.ArtifactID) == "" {
		return AttachmentRecord{}, fmt.Errorf("artifact_id required")
	}
	if strings.TrimSpace(rec.StorageURI) == "" {
		return AttachmentRecord{}, fmt.Errorf("storage_uri required")
	}
	metadata := defaultJSON(rec.Metadata)
	var retention interface{}
	if rec.RetentionExpiresAt != nil && !rec.RetentionExpiresAt.IsZero() {
		retention = rec.RetentionExpiresAt.UTC()
	}

	row := s.DB.QueryRowContext(ctx, `
INSERT INTO attachments (run_id, task_id, attempt, artifact_id, name, media_type, size_bytes, checksum, storage_uri, retention_expires_at, metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id, run_id, task_id, attempt, artifact_id,
        COALESCE(name,''), COALESCE(media_type,''), COALESCE(size_bytes,0),
        COALESCE(checksum,''), storage_uri, retention_expires_at, metadata, created_at, updated_at
`, rec.RunID, rec.TaskID, rec.Attempt, rec.ArtifactID, nullableString(strings.TrimSpace(rec.Name)), nullableString(strings.TrimSpace(rec.MediaType)), nullableInt64(rec.SizeBytes), nullableString(strings.TrimSpace(rec.Checksum)), rec.StorageURI, retention, metadata)

	var retentionTime sql.NullTime
	if err := row.Scan(&rec.ID, &rec.RunID, &rec.TaskID, &rec.Attempt, &rec.ArtifactID, &rec.Name, &rec.MediaType, &rec.SizeBytes, &rec.Checksum, &rec.StorageURI, &retentionTime, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return AttachmentRecord{}, err
	}
	if retentionTime.Valid {
		ts := retentionTime.Time
		rec.RetentionExpiresAt = &ts
	}
	return rec, nil
}

// ListAttachmentsByRun returns all attachments associated with a run.
func (s *Store) ListAttachmentsByRun(ctx context.Context, runID string) ([]AttachmentRecord, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run_id required")
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, run_id, task_id, attempt, artifact_id,
       COALESCE(name,''), COALESCE(media_type,''), COALESCE(size_bytes,0),
       COALESCE(checksum,''), storage_uri, retention_expires_at, metadata, created_at, updated_at
FROM attachments
WHERE run_id=$1
ORDER BY created_at ASC
`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AttachmentRecord
	for rows.Next() {
		var rec AttachmentRecord
		var retention sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.RunID, &rec.TaskID, &rec.Attempt, &rec.ArtifactID, &rec.Name, &rec.MediaType, &rec.SizeBytes, &rec.Checksum, &rec.StorageURI, &retention, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		if retention.Valid {
			ts := retention.Time
			rec.RetentionExpiresAt = &ts
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteAttachment removes a stored attachment metadata entry.
func (s *Store) DeleteAttachment(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("attachment id required")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM attachments WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	} else if err != nil {
		return err
	}
	return nil
}

// Episode captures the full episodic memory snapshot for a run.
type Episode struct {
	ID           string
	RunID        string
	TopicID      string
	UserID       string
	Thought      core.UserThought
	PlanDocument *planner.PlanDocument
	PlanRaw      json.RawMessage
	PlanPrompt   string
	Result       core.ProcessingResult
	Steps        []EpisodeStep
	CreatedAt    time.Time
}

// EpisodeStep records a single agent execution within an episode trace.
type EpisodeStep struct {
	StepIndex     int
	Task          core.AgentTask
	InputSnapshot map[string]interface{}
	Prompt        string
	Result        core.AgentResult
	Artifacts     []map[string]interface{}
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
}

// EpisodeSummary provides a lightweight view of recorded episodes.
type EpisodeSummary struct {
	RunID     string
	TopicID   string
	UserID    string
	Status    string
	StartedAt *time.Time
	CreatedAt time.Time
	Steps     int
}

// EpisodeFilter constrains ListEpisodes queries.
type EpisodeFilter struct {
	RunID   string
	TopicID string
	Status  string
	From    time.Time
	To      time.Time
	Limit   int
}

type BudgetApprovalRecord struct {
	RunID         string
	TopicID       string
	EstimatedCost float64
	Threshold     float64
	RequestedBy   string
	Status        string
	CreatedAt     time.Time
	DecidedAt     *time.Time
	DecidedBy     *string
	Reason        *string
}

// BudgetEventRecord captures breach and override audit events for budgets.
type BudgetEventRecord struct {
	ID        int64
	RunID     string
	TopicID   string
	EventType string
	Details   map[string]interface{}
	CreatedBy *string
	CreatedAt time.Time
}

// BuilderSchemaRecord captures a conversational builder schema version.
type BuilderSchemaRecord struct {
	ID        string
	TopicID   string
	Kind      string
	Version   int
	Content   json.RawMessage
	AuthorID  *string
	CreatedAt time.Time
}

// RunManifestRecord captures a persisted signed manifest for a run.
type RunManifestRecord struct {
	RunID     string
	TopicID   string
	Manifest  json.RawMessage
	Checksum  string
	Signature string
	Algorithm string
	SignedAt  time.Time
	CreatedAt time.Time
}

// ErrRunManifestExists indicates a manifest already exists with different contents.
var ErrRunManifestExists = errors.New("run manifest already exists")

var (
	metricsOnce    sync.Once
	costCounter    otelmetric.Float64Counter
	tokenCounter   otelmetric.Int64Counter
	metricsInitErr error
)

func initStoreMetrics() {
	meter := otel.Meter("store")
	var err error
	costCounter, err = meter.Float64Counter("processing_cost_total")
	if err != nil {
		metricsInitErr = err
		return
	}
	tokenCounter, err = meter.Int64Counter("processing_tokens_total")
	if err != nil {
		metricsInitErr = err
	}
}

func New(ctx context.Context) (*Store, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		host := getenvDefault("POSTGRES_HOST", "localhost")
		port := getenvDefault("POSTGRES_PORT", "5432")
		user := os.Getenv("POSTGRES_USER")
		pass := os.Getenv("POSTGRES_PASSWORD")
		db := os.Getenv("POSTGRES_DB")
		ssl := getenvDefault("POSTGRES_SSLMODE", "disable")
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, db, ssl)
	}
	return NewWithDSN(ctx, dsn)
}

// NewWithDSN constructs the Store using an explicit Postgres DSN
func NewWithDSN(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func getenvDefault(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func (s *Store) ensureSchema(ctx context.Context) error { return nil }

// User operations
func (s *Store) CreateUser(ctx context.Context, email, hash string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,$2)`, email, hash)
	return err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (id string, hash string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE email=$1`, email).Scan(&id, &hash)
	return
}

// Topic operations
func (s *Store) CreateTopic(ctx context.Context, userID, name string, preferences []byte, cron string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `INSERT INTO topics (user_id, name, preferences, schedule_cron) VALUES ($1,$2,$3,$4) RETURNING id`, userID, name, preferences, cron).Scan(&id)
	return id, err
}

type Topic struct {
	ID           string
	UserID       string
	Name         string
	ScheduleCron string
	CreatedAt    time.Time
}

func (s *Store) ListTopics(ctx context.Context, userID string) ([]Topic, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, schedule_cron, created_at FROM topics WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.ScheduleCron, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTopicByID(ctx context.Context, id string, userID string) (name string, preferences []byte, scheduleCron string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT name, preferences, schedule_cron FROM topics WHERE id=$1 AND user_id=$2`, id, userID).Scan(&name, &preferences, &scheduleCron)
	return
}

// UpsertUpdatePolicy creates or updates the temporal policy associated with a topic.
func (s *Store) UpsertUpdatePolicy(ctx context.Context, topicID string, pol policy.UpdatePolicy) error {
	if topicID == "" {
		return fmt.Errorf("topic_id must be provided")
	}
	if err := pol.Validate(); err != nil {
		return err
	}
	refresh := int64(pol.RefreshInterval / time.Second)
	dedup := int64(pol.DedupWindow / time.Second)
	fresh := int64(pol.FreshnessThreshold / time.Second)
	metadata := pol.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal policy metadata: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO topic_update_policies (topic_id, refresh_interval_seconds, dedup_window_seconds, repeat_mode, freshness_threshold_seconds, metadata, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW())
ON CONFLICT (topic_id) DO UPDATE SET
  refresh_interval_seconds    = EXCLUDED.refresh_interval_seconds,
  dedup_window_seconds        = EXCLUDED.dedup_window_seconds,
  repeat_mode                 = EXCLUDED.repeat_mode,
  freshness_threshold_seconds = EXCLUDED.freshness_threshold_seconds,
  metadata                    = EXCLUDED.metadata,
  updated_at                  = NOW();
`, topicID, refresh, dedup, string(pol.RepeatMode), fresh, metaBytes)
	return err
}

// GetUpdatePolicy fetches the temporal policy for a topic. Bool indicates if a policy exists.
func (s *Store) GetUpdatePolicy(ctx context.Context, topicID string) (policy.UpdatePolicy, bool, error) {
	if topicID == "" {
		return policy.UpdatePolicy{}, false, fmt.Errorf("topic_id must be provided")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT refresh_interval_seconds,
       dedup_window_seconds,
       repeat_mode,
       freshness_threshold_seconds,
       metadata
FROM topic_update_policies
WHERE topic_id=$1
`, topicID)
	var (
		refreshSeconds int64
		dedupSeconds   int64
		freshSeconds   int64
		repeatMode     string
		metadataBytes  []byte
	)
	if err := row.Scan(&refreshSeconds, &dedupSeconds, &repeatMode, &freshSeconds, &metadataBytes); err != nil {
		if err == sql.ErrNoRows {
			return policy.UpdatePolicy{}, false, nil
		}
		return policy.UpdatePolicy{}, false, err
	}
	var metadata map[string]interface{}
	if len(metadataBytes) > 0 {
		_ = json.Unmarshal(metadataBytes, &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	pol := policy.UpdatePolicy{
		RefreshInterval:    time.Duration(refreshSeconds) * time.Second,
		DedupWindow:        time.Duration(dedupSeconds) * time.Second,
		RepeatMode:         policy.RepeatMode(repeatMode),
		FreshnessThreshold: time.Duration(freshSeconds) * time.Second,
		Metadata:           metadata,
	}
	if err := pol.Validate(); err != nil {
		return policy.UpdatePolicy{}, false, err
	}
	return pol, true, nil
}

// UpsertTopicBudgetConfig creates or updates the budget config for a topic.
func (s *Store) UpsertTopicBudgetConfig(ctx context.Context, topicID string, cfg budget.Config) error {
	if topicID == "" {
		return fmt.Errorf("topic_id must be provided")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	meta := cfg.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal budget metadata: %w", err)
	}
	var maxCost sql.NullFloat64
	if cfg.MaxCost != nil {
		maxCost = sql.NullFloat64{Float64: *cfg.MaxCost, Valid: true}
	}
	var approvalThresh sql.NullFloat64
	if cfg.ApprovalThreshold != nil {
		approvalThresh = sql.NullFloat64{Float64: *cfg.ApprovalThreshold, Valid: true}
	}
	var maxTokens sql.NullInt64
	if cfg.MaxTokens != nil {
		maxTokens = sql.NullInt64{Int64: *cfg.MaxTokens, Valid: true}
	}
	var maxTime sql.NullInt64
	if cfg.MaxTimeSeconds != nil {
		maxTime = sql.NullInt64{Int64: *cfg.MaxTimeSeconds, Valid: true}
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO topic_budget_configs (topic_id, max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,NOW(),NOW())
ON CONFLICT (topic_id) DO UPDATE SET
  max_cost = EXCLUDED.max_cost,
  max_tokens = EXCLUDED.max_tokens,
  max_time_seconds = EXCLUDED.max_time_seconds,
  approval_threshold = EXCLUDED.approval_threshold,
  require_approval = EXCLUDED.require_approval,
  metadata = EXCLUDED.metadata,
  updated_at = NOW();
`, topicID, maxCost, maxTokens, maxTime, approvalThresh, cfg.RequireApproval, metaBytes)
	return err
}

// GetTopicBudgetConfig fetches budget config for a topic.
func (s *Store) GetTopicBudgetConfig(ctx context.Context, topicID string) (budget.Config, bool, error) {
	if topicID == "" {
		return budget.Config{}, false, fmt.Errorf("topic_id must be provided")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata
FROM topic_budget_configs
WHERE topic_id=$1
`, topicID)
	var (
		maxCost         sql.NullFloat64
		maxTokens       sql.NullInt64
		maxTime         sql.NullInt64
		threshold       sql.NullFloat64
		requireApproval bool
		metaBytes       []byte
	)
	if err := row.Scan(&maxCost, &maxTokens, &maxTime, &threshold, &requireApproval, &metaBytes); err != nil {
		if err == sql.ErrNoRows {
			return budget.Config{}, false, nil
		}
		return budget.Config{}, false, err
	}
	var meta map[string]interface{}
	if len(metaBytes) > 0 {
		_ = json.Unmarshal(metaBytes, &meta)
	}
	cfg := budget.Config{RequireApproval: requireApproval, Metadata: meta}
	if maxCost.Valid {
		v := maxCost.Float64
		cfg.MaxCost = &v
	}
	if maxTokens.Valid {
		v := maxTokens.Int64
		cfg.MaxTokens = &v
	}
	if maxTime.Valid {
		v := maxTime.Int64
		cfg.MaxTimeSeconds = &v
	}
	if threshold.Valid {
		v := threshold.Float64
		cfg.ApprovalThreshold = &v
	}
	return cfg, true, nil
}

// ApplyRunBudget persists the budget snapshot for a run.
func (s *Store) ApplyRunBudget(ctx context.Context, runID string, cfg budget.Config) error {
	if runID == "" {
		return fmt.Errorf("run_id must be provided")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	var maxCost sql.NullFloat64
	if cfg.MaxCost != nil {
		maxCost = sql.NullFloat64{Float64: *cfg.MaxCost, Valid: true}
	}
	var maxTokens sql.NullInt64
	if cfg.MaxTokens != nil {
		maxTokens = sql.NullInt64{Int64: *cfg.MaxTokens, Valid: true}
	}
	var maxTime sql.NullInt64
	if cfg.MaxTimeSeconds != nil {
		maxTime = sql.NullInt64{Int64: *cfg.MaxTimeSeconds, Valid: true}
	}
	var threshold sql.NullFloat64
	if cfg.ApprovalThreshold != nil {
		threshold = sql.NullFloat64{Float64: *cfg.ApprovalThreshold, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
UPDATE runs SET
  budget_cost_limit = $2,
  budget_token_limit = $3,
  budget_time_seconds = $4,
  budget_approval_threshold = $5,
  budget_require_approval = $6
WHERE id = $1
`, runID, maxCost, maxTokens, maxTime, threshold, cfg.RequireApproval)
	return err
}

// UpdateTopicPrefsAndCron updates a topic's preferences and optional cron
func (s *Store) UpdateTopicPrefsAndCron(ctx context.Context, topicID string, userID string, preferences []byte, scheduleCron *string) error {
	if scheduleCron != nil && *scheduleCron != "" {
		_, err := s.DB.ExecContext(ctx, `UPDATE topics SET preferences=$1, schedule_cron=$2 WHERE id=$3 AND user_id=$4`, preferences, *scheduleCron, topicID, userID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE topics SET preferences=$1 WHERE id=$2 AND user_id=$3`, preferences, topicID, userID)
	return err
}

// UpdateTopicName updates only the topic name (user-driven rename)
func (s *Store) UpdateTopicName(ctx context.Context, topicID string, userID string, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE topics SET name=$1 WHERE id=$2 AND user_id=$3`, name, topicID, userID)
	return err
}

func (s *Store) ListAllTopics(ctx context.Context) ([]Topic, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, schedule_cron, created_at FROM topics`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.ScheduleCron, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) LatestRunTime(ctx context.Context, topicID string) (*time.Time, error) {
	var ts *time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(COALESCE(finished_at, started_at)) FROM runs WHERE topic_id=$1`, topicID).Scan(&ts)
	return ts, err
}

type Run struct {
	ID         string
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
	Error      *string
}

func (s *Store) ListRuns(ctx context.Context, topicID string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, status, started_at, finished_at, error FROM runs WHERE topic_id=$1 ORDER BY started_at DESC`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Status, &r.StartedAt, &r.FinishedAt, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetLatestRunID(ctx context.Context, topicID string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM runs WHERE topic_id=$1 ORDER BY started_at DESC LIMIT 1`, topicID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// InsertRunManifest stores a signed manifest for a run. Manifests are immutable; attempting to persist
// a manifest with different checksum/signature will return ErrRunManifestExists.
func (s *Store) InsertRunManifest(ctx context.Context, rec RunManifestRecord) error {
	if rec.RunID == "" {
		return fmt.Errorf("run_id required")
	}
	if rec.TopicID == "" {
		return fmt.Errorf("topic_id required")
	}
	if len(rec.Manifest) == 0 {
		return fmt.Errorf("manifest payload required")
	}
	if rec.Checksum == "" || rec.Signature == "" {
		return fmt.Errorf("checksum and signature required")
	}
	if rec.Algorithm == "" {
		rec.Algorithm = "hmac-sha256"
	}
	res, err := s.DB.ExecContext(ctx, `
INSERT INTO run_manifests (run_id, topic_id, manifest, checksum, signature, algorithm, signed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT DO NOTHING
`, rec.RunID, rec.TopicID, rec.Manifest, rec.Checksum, rec.Signature, rec.Algorithm, rec.SignedAt)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows > 0 {
		return nil
	} else if err != nil {
		return err
	}
	// Manifest already exists; verify it matches input
	existing, ok, err := s.GetRunManifest(ctx, rec.RunID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("run manifest insert conflict but existing record missing")
	}
	if existing.Checksum == rec.Checksum && existing.Signature == rec.Signature {
		return nil
	}
	return ErrRunManifestExists
}

// GetRunManifest fetches the stored signed manifest for a run.
func (s *Store) GetRunManifest(ctx context.Context, runID string) (RunManifestRecord, bool, error) {
	if runID == "" {
		return RunManifestRecord{}, false, fmt.Errorf("run_id required")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at
FROM run_manifests
WHERE run_id=$1
`, runID)
	var rec RunManifestRecord
	var manifestBytes []byte
	if err := row.Scan(&rec.RunID, &rec.TopicID, &manifestBytes, &rec.Checksum, &rec.Signature, &rec.Algorithm, &rec.SignedAt, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return RunManifestRecord{}, false, nil
		}
		return RunManifestRecord{}, false, err
	}
	rec.Manifest = append(json.RawMessage{}, manifestBytes...)
	return rec, true, nil
}

// Processing results persistence bridging Postgres storage in agent
// Provide a simple accessor by id
func (s *Store) GetProcessingResultByID(ctx context.Context, id string) (map[string]interface{}, error) {
	var (
		userThoughtB, sourcesB, highlightsB, conflictsB, agentsB, modelsB, metadataB, evidenceB []byte
		summary, detailed                                                                       string
		confidence                                                                              float64
		processingTime                                                                          int64
		cost                                                                                    float64
		tokensUsed                                                                              int64
		created                                                                                 time.Time
	)
	err := s.DB.QueryRowContext(ctx, `SELECT user_thought, summary, detailed_report, sources, highlights, conflicts,
        confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, evidence, created_at
        FROM processing_results WHERE id=$1`, id).Scan(&userThoughtB, &summary, &detailed, &sourcesB, &highlightsB, &conflictsB,
		&confidence, &processingTime, &cost, &tokensUsed, &agentsB, &modelsB, &metadataB, &evidenceB, &created)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"id":              id,
		"summary":         summary,
		"detailed_report": detailed,
		"confidence":      confidence,
		"processing_time": processingTime,
		"cost_estimate":   cost,
		"tokens_used":     tokensUsed,
		"created_at":      created,
	}
	var v interface{}
	_ = json.Unmarshal(userThoughtB, &v)
	out["user_thought"] = v
	_ = json.Unmarshal(sourcesB, &v)
	out["sources"] = v
	_ = json.Unmarshal(highlightsB, &v)
	out["highlights"] = v
	_ = json.Unmarshal(conflictsB, &v)
	out["conflicts"] = v
	_ = json.Unmarshal(agentsB, &v)
	out["agents_used"] = v
	_ = json.Unmarshal(modelsB, &v)
	out["llm_models_used"] = v
	if len(evidenceB) > 0 {
		_ = json.Unmarshal(evidenceB, &v)
		out["evidence"] = v
	} else {
		out["evidence"] = []interface{}{}
	}
	if len(metadataB) > 0 {
		_ = json.Unmarshal(metadataB, &v)
		out["metadata"] = v
	}
	return out, nil
}

// Run operations
func (s *Store) CreateRun(ctx context.Context, topicID string, status string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `INSERT INTO runs (topic_id, status) VALUES ($1,$2) RETURNING id`, topicID, status).Scan(&id)
	return id, err
}

func (s *Store) FinishRun(ctx context.Context, runID string, status string, errMsg *string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status=$1, finished_at=NOW(), error=$2 WHERE id=$3`, status, errMsg, runID)
	return err
}

// SetRunStatus updates the status field without modifying timestamps.
func (s *Store) SetRunStatus(ctx context.Context, runID string, status string) error {
	if runID == "" {
		return fmt.Errorf("run_id must be provided")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status=$2 WHERE id=$1`, runID, status)
	return err
}

// MarkRunBudgetOverrun flags a run as over budget with a descriptive reason.
func (s *Store) MarkRunBudgetOverrun(ctx context.Context, runID string, reason string) error {
	if runID == "" {
		return fmt.Errorf("run_id must be provided")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET budget_overrun = true, budget_overrun_reason = $2 WHERE id = $1`, runID, reason)
	return err
}

// MarkRunPendingApproval updates run status to pending_approval.
func (s *Store) MarkRunPendingApproval(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("run_id must be provided")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status='pending_approval' WHERE id=$1`, runID)
	return err
}

// CreateBudgetApproval inserts a pending approval entry for a run.
func (s *Store) CreateBudgetApproval(ctx context.Context, runID, topicID, requestedBy string, estimatedCost, threshold float64) error {
	if runID == "" || topicID == "" || requestedBy == "" {
		return fmt.Errorf("run_id, topic_id, and requested_by are required")
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO run_budget_approvals (run_id, topic_id, estimated_cost, approval_threshold, requested_by)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (run_id) DO UPDATE SET
  estimated_cost = EXCLUDED.estimated_cost,
  approval_threshold = EXCLUDED.approval_threshold,
  status = 'pending',
  created_at = NOW(),
  decided_at = NULL,
  decided_by = NULL,
  reason = NULL;
`, runID, topicID, estimatedCost, threshold, requestedBy)
	return err
}

// ResolveBudgetApproval updates the approval status and marks the run accordingly.
func (s *Store) ResolveBudgetApproval(ctx context.Context, runID string, approve bool, decidedBy string, reason *string) error {
	if runID == "" || decidedBy == "" {
		return fmt.Errorf("run_id and decided_by required")
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	_, err := s.DB.ExecContext(ctx, `
UPDATE run_budget_approvals
SET status=$2,
    decided_at=NOW(),
    decided_by=$3,
    reason=$4
WHERE run_id=$1
`, runID, status, decidedBy, reason)
	return err
}

// RecordBudgetEvent persists an arbitrary budget audit event for a run.
func (s *Store) RecordBudgetEvent(ctx context.Context, evt BudgetEventRecord) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialised")
	}
	runID := strings.TrimSpace(evt.RunID)
	if runID == "" {
		return fmt.Errorf("run_id must be provided")
	}
	eventType := strings.TrimSpace(evt.EventType)
	if eventType == "" {
		return fmt.Errorf("event_type must be provided")
	}
	details := evt.Details
	if details == nil {
		details = map[string]interface{}{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal budget event details: %w", err)
	}
	var topicID interface{}
	if topic := strings.TrimSpace(evt.TopicID); topic != "" {
		topicID = topic
	}
	var createdBy interface{}
	if evt.CreatedBy != nil {
		if id := strings.TrimSpace(*evt.CreatedBy); id != "" {
			createdBy = id
		}
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO run_budget_events (run_id, topic_id, event_type, details, created_by)
VALUES ($1,$2,$3,$4,$5)
`, runID, topicID, eventType, payload, createdBy); err != nil {
		return fmt.Errorf("insert budget event: %w", err)
	}
	return nil
}

// RecordBudgetBreach stores a budget breach report with associated usage metrics.
func (s *Store) RecordBudgetBreach(ctx context.Context, runID, topicID string, cfg budget.Config, usage budget.Usage, exceeded budget.ErrExceeded, estimates ...budget.Estimate) error {
	est := budget.Estimate{}
	if len(estimates) > 0 {
		est = estimates[0]
	}
	details := map[string]interface{}{
		"message":        exceeded.Error(),
		"kind":           exceeded.Kind,
		"usage_label":    exceeded.Usage,
		"limit_label":    exceeded.Limit,
		"usage":          budget.SerializeUsage(usage),
		"breach_summary": budget.SerializeBreach(cfg, usage, exceeded),
		"config":         budget.SerializeConfig(cfg),
		"report":         budget.BuildReport(cfg, est, usage, exceeded),
		"reason":         budget.FormatBreachReason(cfg, usage, exceeded),
	}
	return s.RecordBudgetEvent(ctx, BudgetEventRecord{
		RunID:     runID,
		TopicID:   topicID,
		EventType: "breach",
		Details:   details,
	})
}

// RecordBudgetOverride stores manual override/approval audit entries.
func (s *Store) RecordBudgetOverride(ctx context.Context, runID, topicID, decidedBy string, approved bool, reason *string) error {
	if strings.TrimSpace(decidedBy) == "" {
		return fmt.Errorf("decided_by must be provided")
	}
	details := map[string]interface{}{
		"approved": approved,
	}
	if reason != nil {
		if trimmed := strings.TrimSpace(*reason); trimmed != "" {
			details["reason"] = trimmed
		}
	}
	eventType := "override.rejected"
	if approved {
		eventType = "override.approved"
	}
	return s.RecordBudgetEvent(ctx, BudgetEventRecord{
		RunID:     runID,
		TopicID:   topicID,
		EventType: eventType,
		Details:   details,
		CreatedBy: &decidedBy,
	})
}

// GetPendingBudgetApproval returns the pending approval for a topic if one exists.
func (s *Store) GetPendingBudgetApproval(ctx context.Context, topicID string) (BudgetApprovalRecord, bool, error) {
	if topicID == "" {
		return BudgetApprovalRecord{}, false, fmt.Errorf("topic_id required")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT run_id, topic_id, estimated_cost, approval_threshold, requested_by, status, created_at, decided_at, decided_by, COALESCE(reason,'')
FROM run_budget_approvals
WHERE topic_id=$1 AND status='pending'
ORDER BY created_at ASC
LIMIT 1
`, topicID)
	var (
		rec        BudgetApprovalRecord
		reasonText string
	)
	if err := row.Scan(&rec.RunID, &rec.TopicID, &rec.EstimatedCost, &rec.Threshold, &rec.RequestedBy, &rec.Status, &rec.CreatedAt, &rec.DecidedAt, &rec.DecidedBy, &reasonText); err != nil {
		if err == sql.ErrNoRows {
			return BudgetApprovalRecord{}, false, nil
		}
		return BudgetApprovalRecord{}, false, err
	}
	if reasonText != "" {
		rec.Reason = &reasonText
	}
	return rec, true, nil
}

// UpsertProcessingResult saves the agent core ProcessingResult keyed by run ID
func (s *Store) UpsertProcessingResult(ctx context.Context, pr core.ProcessingResult) error {
	toJSON := func(v interface{}) ([]byte, error) { return json.Marshal(v) }
	userThought, _ := toJSON(pr.UserThought)
	sources, _ := toJSON(pr.Sources)
	highlights, _ := toJSON(pr.Highlights)
	conflicts, _ := toJSON(pr.Conflicts)
	evidenceJSON, _ := toJSON(pr.Evidence)
	agents, _ := toJSON(pr.AgentsUsed)
	models, _ := toJSON(pr.LLMModelsUsed)
	metadata, _ := toJSON(pr.Metadata)

	// compute fingerprint as summary + len(sources)
	fp := fmt.Sprintf("%x", len(pr.Sources)) + ":" + pr.Summary
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO processing_results (
  id, user_thought, summary, detailed_report, sources, highlights, conflicts,
  confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, evidence, created_at, fingerprint, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13, $14, $15, NOW(), $16, NOW()
)
ON CONFLICT (id) DO UPDATE SET
  user_thought = EXCLUDED.user_thought,
  summary = EXCLUDED.summary,
  detailed_report = EXCLUDED.detailed_report,
  sources = EXCLUDED.sources,
  highlights = EXCLUDED.highlights,
  conflicts = EXCLUDED.conflicts,
  confidence = EXCLUDED.confidence,
  processing_time = EXCLUDED.processing_time,
  cost_estimate = EXCLUDED.cost_estimate,
  tokens_used = EXCLUDED.tokens_used,
  agents_used = EXCLUDED.agents_used,
  llm_models_used = EXCLUDED.llm_models_used,
  metadata = EXCLUDED.metadata,
  evidence = EXCLUDED.evidence,
  fingerprint = CASE WHEN processing_results.fingerprint = EXCLUDED.fingerprint THEN processing_results.fingerprint ELSE EXCLUDED.fingerprint END,
  updated_at = CASE WHEN processing_results.fingerprint = EXCLUDED.fingerprint THEN processing_results.updated_at ELSE NOW() END;
`,
		pr.ID, userThought, pr.Summary, pr.DetailedReport, sources, highlights, conflicts,
		pr.Confidence, int64(pr.ProcessingTime), pr.CostEstimate, pr.TokensUsed, agents, models, metadata, evidenceJSON, fp,
	)
	if err != nil {
		return err
	}
	if err := s.ReplaceRunEvidence(ctx, pr.ID, pr.Evidence); err != nil {
		return err
	}
	metricsOnce.Do(initStoreMetrics)
	if metricsInitErr == nil {
		attrs := []attribute.KeyValue{
			attribute.String("run_id", pr.ID),
		}
		if costCounter != nil && pr.CostEstimate > 0 {
			costCounter.Add(ctx, pr.CostEstimate, otelmetric.WithAttributes(attrs...))
		}
		if tokenCounter != nil && pr.TokensUsed > 0 {
			tokenCounter.Add(ctx, pr.TokensUsed, otelmetric.WithAttributes(attrs...))
		}
	}
	return nil
}

// SaveKnowledgeGraphFromMetadata extracts knowledge_graph from metadata and persists it for a topic
func (s *Store) SaveKnowledgeGraphFromMetadata(ctx context.Context, topic string, metadata map[string]interface{}) error {
	if metadata == nil {
		return nil
	}
	kgRaw, ok := metadata["knowledge_graph"].(map[string]interface{})
	if !ok {
		return nil
	}
	nodesB, _ := json.Marshal(kgRaw["nodes"])
	edgesB, _ := json.Marshal(kgRaw["edges"])
	metaB, _ := json.Marshal(metadata)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_graphs (topic, nodes, edges, metadata, last_updated) VALUES ($1,$2,$3,$4,NOW())`, topic, nodesB, edgesB, metaB)
	return err
}

// SaveHighlights persists highlights for a topic
func (s *Store) SaveHighlights(ctx context.Context, topic string, hs []core.Highlight) error {
	if len(hs) == 0 {
		return nil
	}
	for _, h := range hs {
		sourcesB, _ := json.Marshal(h.Sources)
		_, err := s.DB.ExecContext(ctx, `INSERT INTO highlights (topic, title, content, type, priority, sources, is_pinned, created_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE($8,NOW()),$9)`, topic, h.Title, h.Content, h.Type, h.Priority, sourcesB, h.IsPinned, h.CreatedAt, h.ExpiresAt)
		if err != nil {
			return err
		}
	}
	return nil
}

// ClaimIdempotency attempts to register a processed event. It returns false if the key already exists.
func (s *Store) ClaimIdempotency(ctx context.Context, scope, key string) (bool, error) {
	if scope == "" || key == "" {
		return false, fmt.Errorf("scope and key must be provided")
	}
	var inserted bool
	err := s.DB.QueryRowContext(ctx, `INSERT INTO idempotency_keys (scope, key) VALUES ($1,$2) ON CONFLICT DO NOTHING RETURNING true`, scope, key).Scan(&inserted)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// UpsertCheckpoint persists checkpoint progress for a run stage.
func (s *Store) UpsertCheckpoint(ctx context.Context, cp Checkpoint) error {
	if cp.RunID == "" || cp.Stage == "" {
		return fmt.Errorf("run_id and stage are required")
	}
	payloadBytes, err := json.Marshal(cp.Payload)
	if err != nil {
		return fmt.Errorf("marshal checkpoint payload: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO queue_checkpoints (run_id, stage, checkpoint_token, status, payload, retries, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,NOW())
ON CONFLICT (run_id, stage) DO UPDATE SET
  checkpoint_token = EXCLUDED.checkpoint_token,
  status           = EXCLUDED.status,
  payload          = EXCLUDED.payload,
  retries          = EXCLUDED.retries,
  updated_at       = NOW();
`, cp.RunID, cp.Stage, cp.CheckpointToken, cp.Status, payloadBytes, cp.Retries)
	return err
}

// GetCheckpoint retrieves a checkpoint for a run/stage. The bool indicates whether a record was found.
func (s *Store) GetCheckpoint(ctx context.Context, runID, stage string) (Checkpoint, bool, error) {
	var (
		payloadBytes []byte
		cp           Checkpoint
	)
	row := s.DB.QueryRowContext(ctx, `
SELECT run_id::text, stage, status, checkpoint_token, payload, retries, updated_at
FROM queue_checkpoints
WHERE run_id = $1 AND stage = $2`, runID, stage)
	if err := row.Scan(&cp.RunID, &cp.Stage, &cp.Status, &cp.CheckpointToken, &payloadBytes, &cp.Retries, &cp.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Checkpoint{}, false, nil
		}
		return Checkpoint{}, false, err
	}
	if len(payloadBytes) > 0 {
		var m map[string]interface{}
		_ = json.Unmarshal(payloadBytes, &m)
		cp.Payload = m
	}
	return cp, true, nil
}

// ListCheckpointsByStatus returns checkpoints matching any of the provided statuses.
func (s *Store) ListCheckpointsByStatus(ctx context.Context, statuses ...string) ([]Checkpoint, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT run_id::text, stage, status, checkpoint_token, payload, retries, updated_at
FROM queue_checkpoints
WHERE status = ANY($1)`, pq.Array(statuses))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var (
			cp           Checkpoint
			payloadBytes []byte
		)
		if err := rows.Scan(&cp.RunID, &cp.Stage, &cp.Status, &cp.CheckpointToken, &payloadBytes, &cp.Retries, &cp.UpdatedAt); err != nil {
			return nil, err
		}
		if len(payloadBytes) > 0 {
			var m map[string]interface{}
			_ = json.Unmarshal(payloadBytes, &m)
			cp.Payload = m
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// MarkCheckpointStatus updates the checkpoint status for a run stage.
func (s *Store) MarkCheckpointStatus(ctx context.Context, runID, stage, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE queue_checkpoints SET status=$3, updated_at=NOW() WHERE run_id=$1 AND stage=$2`, runID, stage, status)
	return err
}

// UpsertMessageSchema stores or updates a schema definition, recalculating checksum.
func (s *Store) UpsertMessageSchema(ctx context.Context, eventType, version string, schemaBytes []byte) error {
	if eventType == "" || version == "" {
		return fmt.Errorf("eventType and version are required")
	}
	if len(schemaBytes) == 0 {
		return fmt.Errorf("schemaBytes is empty")
	}
	h := sha256.Sum256(schemaBytes)
	checksum := hex.EncodeToString(h[:])
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO message_schemas (event_type, version, schema, checksum, created_at)
VALUES ($1,$2,$3,$4,NOW())
ON CONFLICT (event_type, version) DO UPDATE SET
  schema = EXCLUDED.schema,
  checksum = EXCLUDED.checksum,
  created_at = message_schemas.created_at;
`, eventType, version, schemaBytes, checksum)
	return err
}

// ListMessageSchemas returns all stored schema definitions.
func (s *Store) ListMessageSchemas(ctx context.Context) ([]SchemaRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT event_type, version, schema, checksum, created_at FROM message_schemas ORDER BY event_type, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchemaRecord
	for rows.Next() {
		var rec SchemaRecord
		if err := rows.Scan(&rec.EventType, &rec.Version, &rec.Schema, &rec.Checksum, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GetMessageSchema fetches a schema for the provided event type/version.
func (s *Store) GetMessageSchema(ctx context.Context, eventType, version string) (SchemaRecord, bool, error) {
	var rec SchemaRecord
	row := s.DB.QueryRowContext(ctx, `SELECT event_type, version, schema, checksum, created_at FROM message_schemas WHERE event_type=$1 AND version=$2`, eventType, version)
	if err := row.Scan(&rec.EventType, &rec.Version, &rec.Schema, &rec.Checksum, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return SchemaRecord{}, false, nil
		}
		return SchemaRecord{}, false, err
	}
	return rec, true, nil
}

// UpsertToolCard stores or updates a ToolCard definition.
func (s *Store) UpsertToolCard(ctx context.Context, tc ToolCardRecord) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO tool_registry (name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
ON CONFLICT (name, version) DO UPDATE SET
  description = EXCLUDED.description,
  agent_type = EXCLUDED.agent_type,
  input_schema = EXCLUDED.input_schema,
  output_schema = EXCLUDED.output_schema,
  cost_estimate = EXCLUDED.cost_estimate,
  side_effects = EXCLUDED.side_effects,
  checksum = EXCLUDED.checksum,
  signature = EXCLUDED.signature;
`, tc.Name, tc.Version, tc.Description, tc.AgentType, tc.InputSchema, tc.OutputSchema, tc.CostEstimate, tc.SideEffects, tc.Checksum, tc.Signature)
	return err
}

// ListToolCards returns all registered ToolCards.
func (s *Store) ListToolCards(ctx context.Context) ([]ToolCardRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at FROM tool_registry ORDER BY name, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCardRecord
	for rows.Next() {
		var rec ToolCardRecord
		if err := rows.Scan(&rec.Name, &rec.Version, &rec.Description, &rec.AgentType, &rec.InputSchema, &rec.OutputSchema, &rec.CostEstimate, &rec.SideEffects, &rec.Checksum, &rec.Signature, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GetToolCard fetches a ToolCard by name/version.
func (s *Store) GetToolCard(ctx context.Context, name, version string) (ToolCardRecord, bool, error) {
	var rec ToolCardRecord
	row := s.DB.QueryRowContext(ctx, `SELECT name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at FROM tool_registry WHERE name=$1 AND version=$2`, name, version)
	if err := row.Scan(&rec.Name, &rec.Version, &rec.Description, &rec.AgentType, &rec.InputSchema, &rec.OutputSchema, &rec.CostEstimate, &rec.SideEffects, &rec.Checksum, &rec.Signature, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return ToolCardRecord{}, false, nil
		}
		return ToolCardRecord{}, false, err
	}
	return rec, true, nil
}

// SavePlanGraph upserts a planner graph document by plan_id.
func (s *Store) SavePlanGraph(ctx context.Context, rec PlanGraphRecord) error {
	if rec.PlanID == "" {
		return fmt.Errorf("plan_id is required")
	}
	if rec.ThoughtID == "" {
		return fmt.Errorf("thought_id is required")
	}
	if len(rec.PlanJSON) == 0 {
		return fmt.Errorf("plan_json is required")
	}
	if err := planner.ValidatePlanDocument(rec.PlanJSON); err != nil {
		return fmt.Errorf("plan_json invalid: %w", err)
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO plan_graphs (plan_id, thought_id, version, confidence, execution_order, budget, estimates, plan_json, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW(),NOW())
ON CONFLICT (plan_id) DO UPDATE SET
  thought_id = EXCLUDED.thought_id,
  version = EXCLUDED.version,
  confidence = EXCLUDED.confidence,
  execution_order = EXCLUDED.execution_order,
  budget = EXCLUDED.budget,
  estimates = EXCLUDED.estimates,
  plan_json = EXCLUDED.plan_json,
  updated_at = NOW();
`, rec.PlanID, rec.ThoughtID, rec.Version, rec.Confidence, pq.Array(rec.ExecutionOrder), rec.Budget, rec.Estimates, rec.PlanJSON)
	return err
}

// GetLatestPlanGraph returns the most recent plan graph for a thought.
func (s *Store) GetLatestPlanGraph(ctx context.Context, thoughtID string) (PlanGraphRecord, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT plan_id, thought_id, version, confidence, execution_order, budget, estimates, plan_json, created_at, updated_at
FROM plan_graphs
WHERE thought_id=$1
ORDER BY updated_at DESC
LIMIT 1
`, thoughtID)
	var (
		rec       PlanGraphRecord
		execOrder pq.StringArray
	)
	if err := row.Scan(&rec.PlanID, &rec.ThoughtID, &rec.Version, &rec.Confidence, &execOrder, &rec.Budget, &rec.Estimates, &rec.PlanJSON, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return PlanGraphRecord{}, false, nil
		}
		return PlanGraphRecord{}, false, err
	}
	rec.ExecutionOrder = []string(execOrder)
	return rec, true, nil
}

// SaveBuilderSchema persists a new schema version for the supplied topic and kind.
func (s *Store) SaveBuilderSchema(ctx context.Context, topicID, kind, authorID string, content json.RawMessage) (BuilderSchemaRecord, error) {
	if topicID == "" {
		return BuilderSchemaRecord{}, fmt.Errorf("topic_id required")
	}
	if strings.TrimSpace(kind) == "" {
		return BuilderSchemaRecord{}, fmt.Errorf("kind required")
	}
	if len(content) == 0 {
		return BuilderSchemaRecord{}, fmt.Errorf("content required")
	}
	var author interface{}
	if strings.TrimSpace(authorID) != "" {
		author = authorID
	} else {
		author = nil
	}
	row := s.DB.QueryRowContext(ctx, `
WITH next_version AS (
    SELECT COALESCE(MAX(version), 0) + 1 AS version
    FROM builder_schemas
    WHERE topic_id = $1 AND kind = $2
)
INSERT INTO builder_schemas (id, topic_id, kind, version, content, author_id)
SELECT gen_random_uuid(), $1, $2, version, $3, $4 FROM next_version
RETURNING id, topic_id, kind, version, content, author_id, created_at
`, topicID, kind, content, author)
	var rec BuilderSchemaRecord
	var raw []byte
	var authorNull sql.NullString
	if err := row.Scan(&rec.ID, &rec.TopicID, &rec.Kind, &rec.Version, &raw, &authorNull, &rec.CreatedAt); err != nil {
		return BuilderSchemaRecord{}, err
	}
	rec.Content = append(json.RawMessage{}, raw...)
	if authorNull.Valid {
		val := authorNull.String
		rec.AuthorID = &val
	}
	return rec, nil
}

// GetLatestBuilderSchema returns the most recent schema for a topic/kind.
func (s *Store) GetLatestBuilderSchema(ctx context.Context, topicID, kind string) (BuilderSchemaRecord, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2
ORDER BY version DESC
LIMIT 1
`, topicID, kind)
	return scanBuilderSchema(row)
}

// GetBuilderSchemaByVersion fetches a specific schema version.
func (s *Store) GetBuilderSchemaByVersion(ctx context.Context, topicID, kind string, version int) (BuilderSchemaRecord, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2 AND version=$3
`, topicID, kind, version)
	return scanBuilderSchema(row)
}

// ListBuilderSchemaHistory returns recent schema versions ordered by descending version.
func (s *Store) ListBuilderSchemaHistory(ctx context.Context, topicID, kind string, limit int) ([]BuilderSchemaRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2
ORDER BY version DESC
LIMIT $3
`, topicID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuilderSchemaRecord
	for rows.Next() {
		rec, _, err := scanBuilderSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanBuilderSchema(row interface {
	Scan(dest ...interface{}) error
}) (BuilderSchemaRecord, bool, error) {
	var rec BuilderSchemaRecord
	var raw []byte
	var author sql.NullString
	if err := row.Scan(&rec.ID, &rec.TopicID, &rec.Kind, &rec.Version, &raw, &author, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return BuilderSchemaRecord{}, false, nil
		}
		return BuilderSchemaRecord{}, false, err
	}
	rec.Content = append(json.RawMessage{}, raw...)
	if author.Valid {
		val := author.String
		rec.AuthorID = &val
	}
	return rec, true, nil
}

// UpsertRunEmbedding stores or updates the semantic vector for a run-level artifact (e.g., summary).
func (s *Store) UpsertRunEmbedding(ctx context.Context, rec RunEmbeddingRecord) error {
	if rec.RunID == "" {
		return fmt.Errorf("run_id required")
	}
	if rec.TopicID == "" {
		return fmt.Errorf("topic_id required")
	}
	if len(rec.Vector) == 0 {
		return fmt.Errorf("embedding vector required")
	}
	vectorLiteral, err := encodeVectorLiteral(rec.Vector)
	if err != nil {
		return err
	}
	meta := rec.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if rec.Kind == "" {
		rec.Kind = "run_summary"
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO run_embeddings (run_id, topic_id, kind, embedding, metadata, created_at)
VALUES ($1,$2,$3,$4::vector,$5,NOW())
ON CONFLICT (run_id, kind) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  embedding = EXCLUDED.embedding,
  metadata = EXCLUDED.metadata,
  created_at = NOW();
`, rec.RunID, rec.TopicID, rec.Kind, vectorLiteral, metaBytes)
	return err
}

// ReplacePlanStepEmbeddings replaces all plan-step embeddings for a run with the provided records.
func (s *Store) ReplacePlanStepEmbeddings(ctx context.Context, runID string, records []PlanStepEmbeddingRecord) error {
	if runID == "" {
		return fmt.Errorf("run_id required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM plan_step_embeddings WHERE run_id=$1`, runID); err != nil {
		return fmt.Errorf("delete existing plan step embeddings: %w", err)
	}
	if len(records) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO plan_step_embeddings (run_id, topic_id, task_id, kind, embedding, metadata, created_at)
VALUES ($1,$2,$3,$4,$5::vector,$6,NOW())
ON CONFLICT (run_id, task_id, kind) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  embedding = EXCLUDED.embedding,
  metadata = EXCLUDED.metadata,
  created_at = NOW();
`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, rec := range records {
		if rec.TopicID == "" {
			return fmt.Errorf("topic_id required for plan step embedding")
		}
		if rec.TaskID == "" {
			return fmt.Errorf("task_id required for plan step embedding")
		}
		if len(rec.Vector) == 0 {
			return fmt.Errorf("embedding vector required for task %s", rec.TaskID)
		}
		vectorLiteral, err := encodeVectorLiteral(rec.Vector)
		if err != nil {
			return err
		}
		meta := rec.Metadata
		if meta == nil {
			meta = map[string]interface{}{}
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		kind := rec.Kind
		if kind == "" {
			kind = "plan_step"
		}
		if _, err := stmt.ExecContext(ctx, rec.RunID, rec.TopicID, rec.TaskID, kind, vectorLiteral, metaBytes); err != nil {
			return err
		}
	}
	return nil
}

// SearchRunEmbeddings returns the closest run-level embeddings for the supplied vector.
func (s *Store) SearchRunEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]RunEmbeddingSearchResult, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("vector must not be empty")
	}
	if topK <= 0 {
		topK = 5
	}
	vecLiteral, err := encodeVectorLiteral(vector)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT run_id, topic_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM run_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`, vecLiteral, topicID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []RunEmbeddingSearchResult
	for rows.Next() {
		var (
			res       RunEmbeddingSearchResult
			metaBytes []byte
		)
		if err := rows.Scan(&res.RunID, &res.TopicID, &res.Kind, &metaBytes, &res.CreatedAt, &res.Distance); err != nil {
			return nil, err
		}
		if threshold > 0 && res.Distance > threshold {
			continue
		}
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &res.Metadata)
		}
		results = append(results, res)
	}
	return results, rows.Err()
}

// SearchPlanStepEmbeddings returns the closest plan-step embeddings for the supplied vector.
func (s *Store) SearchPlanStepEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]PlanStepEmbeddingSearchResult, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("vector must not be empty")
	}
	if topK <= 0 {
		topK = 10
	}
	vecLiteral, err := encodeVectorLiteral(vector)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT run_id, topic_id, task_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM plan_step_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`, vecLiteral, topicID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []PlanStepEmbeddingSearchResult
	for rows.Next() {
		var (
			res       PlanStepEmbeddingSearchResult
			metaBytes []byte
		)
		if err := rows.Scan(&res.RunID, &res.TopicID, &res.TaskID, &res.Kind, &metaBytes, &res.CreatedAt, &res.Distance); err != nil {
			return nil, err
		}
		if threshold > 0 && res.Distance > threshold {
			continue
		}
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &res.Metadata)
		}
		results = append(results, res)
	}
	return results, rows.Err()
}

// HasSimilarRunEmbedding reports whether a run-level embedding already exists for the topic within
// the supplied similarity threshold and dedup window.
func (s *Store) HasSimilarRunEmbedding(ctx context.Context, topicID string, vector []float32, threshold float64, window time.Duration) (bool, error) {
	if len(vector) == 0 {
		return false, fmt.Errorf("vector must not be empty")
	}
	vecLiteral, err := encodeVectorLiteral(vector)
	if err != nil {
		return false, err
	}
	if threshold <= 0 {
		threshold = 0.8
	}
	maxDistance := math.Max(0, 1-threshold)
	windowSeconds := int64(window / time.Second)
	row := s.DB.QueryRowContext(ctx, `
SELECT 1
FROM run_embeddings
WHERE ($2 = '' OR topic_id = $2)
  AND ($4 <= 0 OR created_at >= NOW() - make_interval(secs => $4))
  AND embedding <=> $1::vector <= $3
LIMIT 1
`, vecLiteral, topicID, maxDistance, windowSeconds)
	var exists int
	switch err := row.Scan(&exists); err {
	case nil:
		return true, nil
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, err
	}
}

// HasSimilarPlanStepEmbedding returns true when a plan-step embedding matches the supplied vector.
func (s *Store) HasSimilarPlanStepEmbedding(ctx context.Context, topicID string, vector []float32, threshold float64, window time.Duration) (bool, error) {
	if len(vector) == 0 {
		return false, fmt.Errorf("vector must not be empty")
	}
	vecLiteral, err := encodeVectorLiteral(vector)
	if err != nil {
		return false, err
	}
	if threshold <= 0 {
		threshold = 0.8
	}
	maxDistance := math.Max(0, 1-threshold)
	windowSeconds := int64(window / time.Second)
	row := s.DB.QueryRowContext(ctx, `
SELECT 1
FROM plan_step_embeddings
WHERE ($2 = '' OR topic_id = $2)
  AND ($4 <= 0 OR created_at >= NOW() - make_interval(secs => $4))
  AND embedding <=> $1::vector <= $3
LIMIT 1
`, vecLiteral, topicID, maxDistance, windowSeconds)
	var exists int
	switch err := row.Scan(&exists); err {
	case nil:
		return true, nil
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, err
	}
}

// LogMemoryDelta records an aggregated delta result for auditing and observability.
func (s *Store) LogMemoryDelta(ctx context.Context, rec MemoryDeltaRecord) error {
	if strings.TrimSpace(rec.TopicID) == "" {
		return fmt.Errorf("topic_id required")
	}
	if rec.TotalItems < 0 || rec.NovelItems < 0 || rec.DuplicateItems < 0 {
		return fmt.Errorf("delta counts must be non-negative")
	}
	if rec.Metadata == nil {
		rec.Metadata = map[string]interface{}{}
	}
	metaBytes, err := json.Marshal(rec.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO memory_delta_events (topic_id, total_items, novel_items, duplicate_items, semantic_enabled, metadata, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
`, rec.TopicID, rec.TotalItems, rec.NovelItems, rec.DuplicateItems, rec.Semantic, metaBytes, createdAt)
	return err
}

// SaveEpisode persists or updates an episodic memory snapshot for a run.
func (s *Store) SaveEpisode(ctx context.Context, ep Episode) (err error) {
	if ep.RunID == "" {
		return fmt.Errorf("run_id required")
	}
	if ep.TopicID == "" {
		return fmt.Errorf("topic_id required")
	}
	if ep.UserID == "" {
		return fmt.Errorf("user_id required")
	}

	thoughtBytes, err := json.Marshal(ep.Thought)
	if err != nil {
		return fmt.Errorf("marshal thought: %w", err)
	}

	var planDocBytes []byte
	if ep.PlanDocument != nil {
		planDocBytes, err = json.Marshal(ep.PlanDocument)
		if err != nil {
			return fmt.Errorf("marshal plan document: %w", err)
		}
	}
	planRawBytes := []byte(ep.PlanRaw)
	if len(planRawBytes) == 0 && len(planDocBytes) > 0 {
		planRawBytes = planDocBytes
	}

	resultBytes, err := json.Marshal(ep.Result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	rawPlanDocArg := interface{}(nil)
	if len(planDocBytes) > 0 {
		rawPlanDocArg = planDocBytes
	}
	rawPlanArg := interface{}(nil)
	if len(planRawBytes) > 0 {
		rawPlanArg = planRawBytes
	}

	row := tx.QueryRowContext(ctx, `
INSERT INTO run_episodes (run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (run_id) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  user_id = EXCLUDED.user_id,
  thought = EXCLUDED.thought,
  plan_document = EXCLUDED.plan_document,
  plan_raw = EXCLUDED.plan_raw,
  plan_prompt = EXCLUDED.plan_prompt,
  result = EXCLUDED.result
RETURNING id, created_at;
`, ep.RunID, ep.TopicID, ep.UserID, thoughtBytes, rawPlanDocArg, rawPlanArg, ep.PlanPrompt, resultBytes)

	var episodeID string
	if err := row.Scan(&episodeID, &ep.CreatedAt); err != nil {
		return fmt.Errorf("upsert run_episodes: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM run_episode_steps WHERE episode_id=$1`, episodeID); err != nil {
		return fmt.Errorf("delete episode steps: %w", err)
	}

	for idx, step := range ep.Steps {
		if step.StepIndex == 0 {
			step.StepIndex = idx
		}
		taskBytes, err := json.Marshal(step.Task)
		if err != nil {
			return fmt.Errorf("marshal step task: %w", err)
		}
		var inputBytes []byte
		if step.InputSnapshot != nil {
			inputBytes, err = json.Marshal(step.InputSnapshot)
			if err != nil {
				return fmt.Errorf("marshal step input: %w", err)
			}
		}
		resultBytes, err := json.Marshal(step.Result)
		if err != nil {
			return fmt.Errorf("marshal step result: %w", err)
		}
		var artifactsBytes []byte
		if step.Artifacts != nil {
			artifactsBytes, err = json.Marshal(step.Artifacts)
			if err != nil {
				return fmt.Errorf("marshal step artifacts: %w", err)
			}
		}
		toInterface := func(b []byte) interface{} {
			if len(b) == 0 {
				return nil
			}
			return b
		}
		var started, completed sql.NullTime
		if step.StartedAt != nil && !step.StartedAt.IsZero() {
			started = sql.NullTime{Time: *step.StartedAt, Valid: true}
		}
		if step.CompletedAt != nil && !step.CompletedAt.IsZero() {
			completed = sql.NullTime{Time: *step.CompletedAt, Valid: true}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO run_episode_steps (episode_id, step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
`, episodeID, step.StepIndex, taskBytes, toInterface(inputBytes), nullableString(step.Prompt), resultBytes, toInterface(artifactsBytes), started, completed); err != nil {
			return fmt.Errorf("insert episode step: %w", err)
		}
	}

	if err := s.replaceRunEvidenceTx(ctx, tx, ep.RunID, ep.Result.Evidence); err != nil {
		return fmt.Errorf("replace run evidence: %w", err)
	}

	return nil
}

func (s *Store) replaceRunEvidenceTx(ctx context.Context, tx *sql.Tx, runID string, evidence []core.Evidence) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run_id required")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM run_evidence WHERE run_id=$1`, runID); err != nil {
		return fmt.Errorf("delete run evidence: %w", err)
	}
	if len(evidence) == 0 {
		return nil
	}
	for _, ev := range evidence {
		if len(ev.SourceIDs) == 0 {
			continue
		}
		claimID := strings.TrimSpace(ev.ID)
		if claimID == "" {
			claimID = uuid.NewString()
		}
		statement := strings.TrimSpace(ev.Statement)
		sourceIDsBytes, err := json.Marshal(ev.SourceIDs)
		if err != nil {
			return fmt.Errorf("marshal source ids: %w", err)
		}
		meta := make(map[string]interface{}, len(ev.Metadata)+2)
		for k, v := range ev.Metadata {
			meta[k] = v
		}
		if ev.Category != "" {
			meta["category"] = ev.Category
		}
		if ev.Score != 0 {
			meta["score"] = ev.Score
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal evidence metadata: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO run_evidence (run_id, claim_id, statement, source_ids, metadata)
VALUES ($1,$2,$3,$4,$5)
`, runID, nullableString(claimID), nullableString(statement), sourceIDsBytes, metaBytes); err != nil {
			return fmt.Errorf("insert run evidence: %w", err)
		}
	}
	return nil
}

// ReplaceRunEvidence replaces evidence entries for a run outside of a supplied transaction.
func (s *Store) ReplaceRunEvidence(ctx context.Context, runID string, evidence []core.Evidence) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := s.replaceRunEvidenceTx(ctx, tx, runID, evidence); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) getRunEvidence(ctx context.Context, runID string) ([]EvidenceRecord, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id required")
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT claim_id, statement, source_ids, metadata, created_at
FROM run_evidence
WHERE run_id=$1
ORDER BY created_at ASC
`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []EvidenceRecord
	for rows.Next() {
		var (
			rec       EvidenceRecord
			claimID   sql.NullString
			statement sql.NullString
			sourceIDs []byte
			metaBytes []byte
		)
		if err := rows.Scan(&claimID, &statement, &sourceIDs, &metaBytes, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.RunID = runID
		if claimID.Valid {
			rec.ClaimID = strings.TrimSpace(claimID.String)
		}
		if statement.Valid {
			rec.Statement = strings.TrimSpace(statement.String)
		}
		if len(sourceIDs) > 0 {
			var ids []string
			if err := json.Unmarshal(sourceIDs, &ids); err == nil {
				rec.SourceIDs = ids
			}
		}
		if len(metaBytes) > 0 {
			var metadata map[string]interface{}
			if err := json.Unmarshal(metaBytes, &metadata); err == nil {
				rec.Metadata = metadata
			}
		}
		if rec.Metadata == nil {
			rec.Metadata = map[string]interface{}{}
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// GetEpisodeByRunID fetches the stored episode trace for a run.
func (s *Store) GetEpisodeByRunID(ctx context.Context, runID string) (Episode, bool, error) {
	if runID == "" {
		return Episode{}, false, fmt.Errorf("run_id required")
	}
	row := s.DB.QueryRowContext(ctx, `
SELECT id, run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result, created_at
FROM run_episodes
WHERE run_id=$1
`, runID)
	var (
		ep           Episode
		thoughtBytes []byte
		planDocBytes []byte
		planRawBytes []byte
		resultBytes  []byte
	)
	if err := row.Scan(&ep.ID, &ep.RunID, &ep.TopicID, &ep.UserID, &thoughtBytes, &planDocBytes, &planRawBytes, &ep.PlanPrompt, &resultBytes, &ep.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return Episode{}, false, nil
		}
		return Episode{}, false, fmt.Errorf("select run_episodes: %w", err)
	}
	if err := json.Unmarshal(thoughtBytes, &ep.Thought); err != nil {
		return Episode{}, false, fmt.Errorf("unmarshal thought: %w", err)
	}
	if len(planDocBytes) > 0 {
		var doc planner.PlanDocument
		if err := json.Unmarshal(planDocBytes, &doc); err != nil {
			return Episode{}, false, fmt.Errorf("unmarshal plan document: %w", err)
		}
		ep.PlanDocument = &doc
	}
	if len(planRawBytes) > 0 {
		ep.PlanRaw = json.RawMessage(planRawBytes)
	}
	if err := json.Unmarshal(resultBytes, &ep.Result); err != nil {
		return Episode{}, false, fmt.Errorf("unmarshal result: %w", err)
	}

	rows, err := s.DB.QueryContext(ctx, `
SELECT step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at, created_at
FROM run_episode_steps
WHERE episode_id=$1
ORDER BY step_index
`, ep.ID)
	if err != nil {
		return Episode{}, false, fmt.Errorf("select episode steps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			step           EpisodeStep
			taskBytes      []byte
			inputBytes     []byte
			prompt         sql.NullString
			resultStep     []byte
			artifactsBytes []byte
			started        sql.NullTime
			completed      sql.NullTime
		)
		if err := rows.Scan(&step.StepIndex, &taskBytes, &inputBytes, &prompt, &resultStep, &artifactsBytes, &started, &completed, &step.CreatedAt); err != nil {
			return Episode{}, false, fmt.Errorf("scan episode step: %w", err)
		}
		if err := json.Unmarshal(taskBytes, &step.Task); err != nil {
			return Episode{}, false, fmt.Errorf("unmarshal step task: %w", err)
		}
		if len(inputBytes) > 0 {
			if err := json.Unmarshal(inputBytes, &step.InputSnapshot); err != nil {
				return Episode{}, false, fmt.Errorf("unmarshal step input: %w", err)
			}
		}
		if prompt.Valid {
			step.Prompt = prompt.String
		}
		if err := json.Unmarshal(resultStep, &step.Result); err != nil {
			return Episode{}, false, fmt.Errorf("unmarshal step result: %w", err)
		}
		if len(artifactsBytes) > 0 {
			if err := json.Unmarshal(artifactsBytes, &step.Artifacts); err != nil {
				return Episode{}, false, fmt.Errorf("unmarshal step artifacts: %w", err)
			}
		}
		if started.Valid {
			step.StartedAt = &started.Time
		}
		if completed.Valid {
			step.CompletedAt = &completed.Time
		}
		ep.Steps = append(ep.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return Episode{}, false, fmt.Errorf("iterate episode steps: %w", err)
	}
	records, err := s.getRunEvidence(ctx, runID)
	if err != nil {
		return Episode{}, false, fmt.Errorf("load run evidence: %w", err)
	}
	if len(records) > 0 {
		evidence := make([]core.Evidence, 0, len(records))
		for _, rec := range records {
			if len(rec.SourceIDs) == 0 {
				continue
			}
			metadataCopy := make(map[string]interface{}, len(rec.Metadata))
			for k, v := range rec.Metadata {
				metadataCopy[k] = v
			}
			claimID := strings.TrimSpace(rec.ClaimID)
			if claimID == "" {
				claimID = uuid.NewString()
			}
			ev := core.Evidence{
				ID:        claimID,
				Statement: rec.Statement,
				SourceIDs: rec.SourceIDs,
				Metadata:  metadataCopy,
			}
			if cat, ok := metadataCopy["category"].(string); ok && strings.TrimSpace(cat) != "" {
				ev.Category = strings.TrimSpace(cat)
				delete(metadataCopy, "category")
			}
			if score, ok := extractFloat(metadataCopy["score"]); ok {
				ev.Score = score
				delete(metadataCopy, "score")
			}
			evidence = append(evidence, ev)
		}
		if len(evidence) > 0 {
			ep.Result.Evidence = evidence
		}
	}

	return ep, true, nil
}

// ListEpisodes returns episode summaries filtered by the provided criteria.
func (s *Store) ListEpisodes(ctx context.Context, filter EpisodeFilter) ([]EpisodeSummary, error) {
	conditions := []string{"1=1"}
	var args []interface{}
	arg := func(value interface{}) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.RunID != "" {
		conditions = append(conditions, fmt.Sprintf("e.run_id = %s", arg(filter.RunID)))
	}
	if filter.TopicID != "" {
		conditions = append(conditions, fmt.Sprintf("e.topic_id = %s", arg(filter.TopicID)))
	}
	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("COALESCE(r.status,'') = %s", arg(filter.Status)))
	}
	if !filter.From.IsZero() {
		conditions = append(conditions, fmt.Sprintf("e.created_at >= %s", arg(filter.From)))
	}
	if !filter.To.IsZero() {
		conditions = append(conditions, fmt.Sprintf("e.created_at <= %s", arg(filter.To)))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := fmt.Sprintf(`
SELECT e.run_id, e.topic_id, e.user_id, COALESCE(r.status,''), r.started_at, e.created_at, COALESCE(MAX(s.step_index) + 1, 0)
FROM run_episodes e
LEFT JOIN runs r ON r.id = e.run_id
LEFT JOIN run_episode_steps s ON s.episode_id = e.id
WHERE %s
GROUP BY e.id, e.run_id, e.topic_id, e.user_id, r.status, r.started_at, e.created_at
ORDER BY e.created_at DESC
LIMIT %d
`, strings.Join(conditions, " AND "), limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []EpisodeSummary
	for rows.Next() {
		var (
			runID, topicID, userID, status string
			startedAt                      sql.NullTime
			createdAt                      time.Time
			steps                          int
		)
		if err := rows.Scan(&runID, &topicID, &userID, &status, &startedAt, &createdAt, &steps); err != nil {
			return nil, err
		}
		var started *time.Time
		if startedAt.Valid {
			started = &startedAt.Time
		}
		summaries = append(summaries, EpisodeSummary{
			RunID:     runID,
			TopicID:   topicID,
			UserID:    userID,
			Status:    status,
			StartedAt: started,
			CreatedAt: createdAt,
			Steps:     steps,
		})
	}
	return summaries, rows.Err()
}

// PruneEpisodesBefore deletes episodes older than the supplied cutoff timestamp.
func (s *Store) PruneEpisodesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if cutoff.IsZero() {
		return 0, fmt.Errorf("cutoff must be provided")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM run_episodes WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// GetKnowledgeGraph retrieves the most recent knowledge graph for a given topic
func (s *Store) GetKnowledgeGraph(ctx context.Context, topic string) (core.KnowledgeGraph, error) {
	var (
		id        string
		nodesB    []byte
		edgesB    []byte
		metaB     []byte
		updatedAt time.Time
	)
	err := s.DB.QueryRowContext(ctx, `SELECT id, nodes, edges, metadata, last_updated FROM knowledge_graphs WHERE topic=$1 ORDER BY last_updated DESC LIMIT 1`, topic).Scan(&id, &nodesB, &edgesB, &metaB, &updatedAt)
	if err != nil {
		return core.KnowledgeGraph{}, err
	}
	var kg core.KnowledgeGraph
	kg.ID = id
	kg.Topic = topic
	kg.LastUpdated = updatedAt
	_ = json.Unmarshal(nodesB, &kg.Nodes)
	_ = json.Unmarshal(edgesB, &kg.Edges)
	if len(metaB) > 0 {
		_ = json.Unmarshal(metaB, &kg.Metadata)
	}
	return kg, nil
}

// Chat message operations
type ChatMessage struct {
	ID        string
	TopicID   string
	UserID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

func (s *Store) CreateChatMessage(ctx context.Context, topicID, userID, role, content string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `INSERT INTO chat_messages (topic_id, user_id, role, content) VALUES ($1,$2,$3,$4) RETURNING id`, topicID, userID, role, content).Scan(&id)
	return id, err
}

// ListChatMessages returns messages for a topic, newest-first up to limit, optionally before a timestamp
func (s *Store) ListChatMessages(ctx context.Context, topicID, userID string, limit int, before *time.Time) ([]ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	// Ensure topic ownership
	if _, _, _, err := s.GetTopicByID(ctx, topicID, userID); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error
	if before != nil {
		rows, err = s.DB.QueryContext(ctx, `SELECT id, topic_id, user_id, role, content, created_at FROM chat_messages WHERE topic_id=$1 AND created_at < $2 ORDER BY created_at DESC LIMIT $3`, topicID, *before, limit)
	} else {
		rows, err = s.DB.QueryContext(ctx, `SELECT id, topic_id, user_id, role, content, created_at FROM chat_messages WHERE topic_id=$1 ORDER BY created_at DESC LIMIT $2`, topicID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.TopicID, &m.UserID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt64(v int64) interface{} {
	if v <= 0 {
		return nil
	}
	return v
}

func defaultJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func extractFloat(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

func encodeVectorLiteral(vec []float32) (string, error) {
	if len(vec) == 0 {
		return "", fmt.Errorf("vector must not be empty")
	}
	var builder strings.Builder
	builder.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String(), nil
}

func decodeVectorLiteral(lit string) ([]float32, error) {
	lit = strings.TrimSpace(lit)
	if lit == "" {
		return nil, fmt.Errorf("empty vector literal")
	}
	lit = strings.TrimPrefix(lit, "[")
	lit = strings.TrimSuffix(lit, "]")
	parts := strings.Split(lit, ",")
	vec := make([]float32, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		f, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector value %q: %w", value, err)
		}
		vec = append(vec, float32(f))
	}
	return vec, nil
}
