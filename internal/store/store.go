package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
)

type Store struct {
	DB *sql.DB
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

// UpdateTopicPrefsAndCron updates a topic's preferences and optional cron
func (s *Store) UpdateTopicPrefsAndCron(ctx context.Context, topicID string, userID string, preferences []byte, scheduleCron *string) error {
	if scheduleCron != nil && *scheduleCron != "" {
		_, err := s.DB.ExecContext(ctx, `UPDATE topics SET preferences=$1, schedule_cron=$2 WHERE id=$3 AND user_id=$4`, preferences, *scheduleCron, topicID, userID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE topics SET preferences=$1 WHERE id=$2 AND user_id=$3`, preferences, topicID, userID)
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
	return err
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
