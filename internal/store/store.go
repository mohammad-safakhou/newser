package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lib/pq"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/budget"
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

// Processing results persistence bridging Postgres storage in agent
// Provide a simple accessor by id
func (s *Store) GetProcessingResultByID(ctx context.Context, id string) (map[string]interface{}, error) {
	var (
		userThoughtB, sourcesB, highlightsB, conflictsB, agentsB, modelsB, metadataB []byte
		summary, detailed                                                            string
		confidence                                                                   float64
		processingTime                                                               int64
		cost                                                                         float64
		tokensUsed                                                                   int64
		created                                                                      time.Time
	)
	err := s.DB.QueryRowContext(ctx, `SELECT user_thought, summary, detailed_report, sources, highlights, conflicts,
        confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at
        FROM processing_results WHERE id=$1`, id).Scan(&userThoughtB, &summary, &detailed, &sourcesB, &highlightsB, &conflictsB,
		&confidence, &processingTime, &cost, &tokensUsed, &agentsB, &modelsB, &metadataB, &created)
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
	agents, _ := toJSON(pr.AgentsUsed)
	models, _ := toJSON(pr.LLMModelsUsed)
	metadata, _ := toJSON(pr.Metadata)

	// compute fingerprint as summary + len(sources)
	fp := fmt.Sprintf("%x", len(pr.Sources)) + ":" + pr.Summary
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO processing_results (
  id, user_thought, summary, detailed_report, sources, highlights, conflicts,
  confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at, fingerprint, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13, $14, NOW(), $15, NOW()
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
  fingerprint = CASE WHEN processing_results.fingerprint = EXCLUDED.fingerprint THEN processing_results.fingerprint ELSE EXCLUDED.fingerprint END,
  updated_at = CASE WHEN processing_results.fingerprint = EXCLUDED.fingerprint THEN processing_results.updated_at ELSE NOW() END;
`,
		pr.ID, userThought, pr.Summary, pr.DetailedReport, sources, highlights, conflicts,
		pr.Confidence, int64(pr.ProcessingTime), pr.CostEstimate, pr.TokensUsed, agents, models, metadata, fp,
	)
	if err != nil {
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
