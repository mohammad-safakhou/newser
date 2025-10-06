package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/manifest"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type runsStubLLMProvider struct {
	vectors [][]float32
	err     error
}

func (p *runsStubLLMProvider) Generate(context.Context, string, string, map[string]interface{}) (string, error) {
	return "", errors.New("not implemented")
}

func (p *runsStubLLMProvider) GenerateWithTokens(context.Context, string, string, map[string]interface{}) (string, int64, int64, error) {
	return "", 0, 0, errors.New("not implemented")
}

func (p *runsStubLLMProvider) Embed(context.Context, string, []string) ([][]float32, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.vectors, nil
}

func (p *runsStubLLMProvider) GetAvailableModels() []string { return nil }

func (p *runsStubLLMProvider) GetModelInfo(string) (core.ModelInfo, error) {
	return core.ModelInfo{}, errors.New("not implemented")
}

func (p *runsStubLLMProvider) CalculateCost(int64, int64, string) float64 { return 0 }

func TestBudgetDecisionApprove(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &RunsHandler{store: &store.Store{DB: db}, cfg: &config.Config{}, orch: nil}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	mock.ExpectQuery(`SELECT run_id, topic_id, estimated_cost`).
		WithArgs("topic").
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "estimated_cost", "approval_threshold", "requested_by", "status", "created_at", "decided_at", "decided_by", "reason"}).AddRow("run", "topic", 30.0, 10.0, "user", "pending", time.Now(), nil, nil, ""))

	mock.ExpectExec(`UPDATE run_budget_approvals SET status=`).
		WithArgs("run", "approved", "user", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`UPDATE runs SET status=`).
		WithArgs("run", "running").
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/topics/topic/runs/run/budget_decision", strings.NewReader(`{"approved": true}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run")
	ctx.Set("user_id", "user")

	if err := handler.budgetDecision(ctx); err != nil {
		t.Fatalf("budgetDecision: %v", err)
	}

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "approved" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBudgetDecisionReject(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &RunsHandler{store: &store.Store{DB: db}, cfg: &config.Config{}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	mock.ExpectQuery(`SELECT run_id, topic_id, estimated_cost`).
		WithArgs("topic").
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "estimated_cost", "approval_threshold", "requested_by", "status", "created_at", "decided_at", "decided_by", "reason"}).AddRow("run", "topic", 30.0, 10.0, "user", "pending", time.Now(), nil, nil, ""))

	mock.ExpectExec(`UPDATE run_budget_approvals SET status=`).
		WithArgs("run", "rejected", "user", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`UPDATE runs SET status=`).
		WithArgs("run", "rejected").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`UPDATE runs SET budget_overrun = true, budget_overrun_reason = \$2 WHERE id = \$1`).
		WithArgs("run", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE runs SET status=$1, finished_at=NOW(), error=$2 WHERE id=$3")).
		WithArgs("rejected", sqlmock.AnyArg(), "run").
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/topics/topic/runs/run/budget_decision", strings.NewReader(`{"approved": false, "reason":"too expensive"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run")
	ctx.Set("user_id", "user")

	if err := handler.budgetDecision(ctx); err != nil {
		t.Fatalf("budgetDecision: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRunsEpisode(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &RunsHandler{store: &store.Store{DB: db}, cfg: &config.Config{}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	thoughtBytes, _ := json.Marshal(core.UserThought{ID: "run-1", TopicID: "topic", UserID: "user", Content: "hello"})
	planBytes, _ := json.Marshal(planner.PlanDocument{Version: "v1"})
	resultBytes, _ := json.Marshal(core.ProcessingResult{ID: "run-1", Summary: "summary"})
	stepTaskBytes, _ := json.Marshal(core.AgentTask{ID: "task-1", Description: "test"})
	stepResultBytes, _ := json.Marshal(core.AgentResult{ID: "task-1_result", TaskID: "task-1", AgentType: "analysis"})
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result, created_at FROM run_episodes WHERE run_id=$1`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "topic_id", "user_id", "thought", "plan_document", "plan_raw", "plan_prompt", "result", "created_at"}).
			AddRow("episode-1", "run-1", "topic", "user", thoughtBytes, planBytes, planBytes, "planning prompt", resultBytes, now))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at, created_at FROM run_episode_steps WHERE episode_id=$1 ORDER BY step_index`)).
		WithArgs("episode-1").
		WillReturnRows(sqlmock.NewRows([]string{"step_index", "task", "input_snapshot", "prompt", "result", "artifacts", "started_at", "completed_at", "created_at"}).
			AddRow(0, stepTaskBytes, nil, "prompt", stepResultBytes, nil, now, now, now))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/runs/run-1/episode", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run-1")
	ctx.Set("user_id", "user")

	if err := handler.episode(ctx); err != nil {
		t.Fatalf("episode: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp episodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RunID != "run-1" || len(resp.Steps) != 1 || resp.Steps[0].Prompt != "prompt" {
		t.Fatalf("unexpected response payload: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPlanExplain(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	handler := NewRunsHandler(cfg, &store.Store{DB: db}, nil, nil, nil)

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	planDoc := planner.PlanDocument{PlanID: "plan-1", Version: "v1", Confidence: 0.8}
	planBytes, _ := json.Marshal(planDoc)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plan_id, thought_id, version, confidence, execution_order, budget, estimates, plan_json, created_at, updated_at FROM plan_graphs WHERE thought_id=$1 ORDER BY updated_at DESC LIMIT 1`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"plan_id", "thought_id", "version", "confidence", "execution_order", "budget", "estimates", "plan_json", "created_at", "updated_at"}).
			AddRow("plan-1", "run-1", "v1", 0.8, pq.StringArray{"task-1"}, []byte(`{}`), []byte(`{}`), planBytes, now, now))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/runs/run-1/plan", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run-1")
	ctx.Set("user_id", "user")

	if err := handler.planExplain(ctx); err != nil {
		t.Fatalf("planExplain: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp planExplainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PlanID != "plan-1" || resp.Plan == nil || resp.Plan.PlanID != "plan-1" {
		t.Fatalf("unexpected plan response: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMemoryHits(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{
		Enabled:         true,
		EmbeddingModel:  "text-embedding",
		SearchTopK:      3,
		SearchThreshold: 0.9,
	}}}
	provider := &runsStubLLMProvider{vectors: [][]float32{{0.1, 0.2}}}
	handler := NewRunsHandler(cfg, &store.Store{DB: db}, nil, provider, nil)

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	userThought := []byte(`{"id":"run-1"}`)
	summary := "Daily summary"
	detailed := "Detailed context"
	sources := []byte(`[{"id":"src-1","title":"Example","url":"https://example.com/a","type":"news"}]`)
	highlights := []byte(`[]`)
	conflicts := []byte(`[]`)
	agentsUsed := []byte(`[
		"analysis"
	]`)
	modelsUsed := []byte(`["gpt"]`)
	metadata := []byte(`{"items":[]}`)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_thought, summary, detailed_report, sources, highlights, conflicts,
        confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at
        FROM processing_results WHERE id=$1`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"user_thought", "summary", "detailed_report", "sources", "highlights", "conflicts", "confidence", "processing_time", "cost_estimate", "tokens_used", "agents_used", "llm_models_used", "metadata", "created_at"}).
			AddRow(userThought, summary, detailed, sources, highlights, conflicts, 0.9, int64(120), 12.5, int64(1000), agentsUsed, modelsUsed, metadata, now))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT run_id, topic_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM run_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`)).
		WithArgs(sqlmock.AnyArg(), "topic", 3).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "kind", "metadata", "created_at", "distance"}).
			AddRow("run-2", "topic", "run_summary", []byte(`{"title":"Prev run"}`), now, 0.2))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT run_id, topic_id, task_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM plan_step_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`)).
		WithArgs(sqlmock.AnyArg(), "topic", 3).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "task_id", "kind", "metadata", "created_at", "distance"}).
			AddRow("run-3", "topic", "task-1", "plan_step", []byte(`{"description":"step"}`), now, 0.35))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/runs/run-1/memory_hits", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run-1")
	ctx.Set("user_id", "user")

	if err := handler.memoryHits(ctx); err != nil {
		t.Fatalf("memoryHits: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp memoryHitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.RunMatches) != 1 || resp.RunMatches[0].RunID != "run-2" {
		t.Fatalf("unexpected run matches: %+v", resp.RunMatches)
	}
	if len(resp.PlanStepMatches) != 1 || resp.PlanStepMatches[0].TaskID != "task-1" {
		t.Fatalf("unexpected plan step matches: %+v", resp.PlanStepMatches)
	}
	if resp.Query != summary {
		t.Fatalf("expected query to default to summary, got %q", resp.Query)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

type sseRecorder struct {
	*httptest.ResponseRecorder
}

func (s *sseRecorder) Flush() {}

func TestStreamRuns(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{Server: config.ServerConfig{RunStreamEnabled: true}}
	handler := NewRunsHandler(cfg, &store.Store{DB: db}, nil, nil, nil)

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("Topic", []byte(`{}`), "@daily"))

	started := time.Now().Add(-5 * time.Minute)
	finished := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, status, started_at, finished_at, error FROM runs WHERE topic_id=$1 ORDER BY started_at DESC`)).
		WithArgs("topic").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "started_at", "finished_at", "error"}).
			AddRow("run-1", "succeeded", started, finished, nil))

	userThought := []byte(`{"id":"run-1"}`)
	sources := []byte(`[]`)
	highlights := []byte(`[]`)
	conflicts := []byte(`[]`)
	agentsUsed := []byte(`[]`)
	modelsUsed := []byte(`[]`)
	metadata := []byte(`{}`)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_thought, summary, detailed_report, sources, highlights, conflicts,
        confidence, processing_time, cost_estimate, tokens_used, agents_used, llm_models_used, metadata, created_at
        FROM processing_results WHERE id=$1`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"user_thought", "summary", "detailed_report", "sources", "highlights", "conflicts", "confidence", "processing_time", "cost_estimate", "tokens_used", "agents_used", "llm_models_used", "metadata", "created_at"}).
			AddRow(userThought, "summary", "details", sources, highlights, conflicts, 0.9, int64(60), 42.5, int64(1000), agentsUsed, modelsUsed, metadata, time.Now()))

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/runs/stream?interval=60", nil)
	req = req.WithContext(streamCtx)
	rec := &sseRecorder{httptest.NewRecorder()}
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic")
	ctx.Set("user_id", "user")

	errCh := make(chan error, 1)
	go func() {
		errCh <- handler.streamRuns(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("streamRuns returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("streamRuns did not return")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: update") {
		t.Fatalf("expected SSE event, got %q", body)
	}
	if !strings.Contains(body, "\"run_id\":\"run-1\"") {
		t.Fatalf("expected run data in stream, got %q", body)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreateRunManifest(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		Server:     config.ServerConfig{RunManifestEnabled: true},
		Capability: config.CapabilityConfig{SigningSecret: "secret"},
	}
	handler := NewRunsHandler(cfg, &store.Store{DB: db}, nil, nil, nil)

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at FROM run_manifests WHERE run_id=$1")).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}))

	thoughtBytes, _ := json.Marshal(core.UserThought{ID: "run-1", TopicID: "topic", UserID: "user", Content: "hello"})
	planBytes, _ := json.Marshal(planner.PlanDocument{Version: "v1"})
	resultBytes, _ := json.Marshal(core.ProcessingResult{
		ID:             "run-1",
		Summary:        "summary",
		DetailedReport: "details",
		Confidence:     0.9,
		AgentsUsed:     []string{"analysis"},
		LLMModelsUsed:  []string{"gpt"},
		CostEstimate:   10,
		TokensUsed:     1000,
		Sources:        []core.Source{{ID: "src-1", Title: "Example", URL: "https://example.com/a", Summary: "example summary"}},
		Metadata: map[string]any{
			"items":        []map[string]any{{"title": "claim", "source_ids": []string{"src-1"}}},
			"digest_stats": map[string]any{"count": 1},
		},
	})
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result, created_at FROM run_episodes WHERE run_id=$1`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "topic_id", "user_id", "thought", "plan_document", "plan_raw", "plan_prompt", "result", "created_at"}).
			AddRow("episode-1", "run-1", "topic", "user", thoughtBytes, planBytes, planBytes, "prompt", resultBytes, now))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at, created_at FROM run_episode_steps WHERE episode_id=$1 ORDER BY step_index`)).
		WithArgs("episode-1").
		WillReturnRows(sqlmock.NewRows([]string{"step_index", "task", "input_snapshot", "prompt", "result", "artifacts", "started_at", "completed_at", "created_at"}))

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO run_manifests (run_id, topic_id, manifest, checksum, signature, algorithm, signed_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING")).
		WithArgs("run-1", "topic", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/topics/topic/runs/run-1/manifest", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run-1")
	ctx.Set("user_id", "user")

	if err := handler.createManifest(ctx); err != nil {
		t.Fatalf("createManifest: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if rec.Header().Get("X-Run-Manifest-Checksum") == "" {
		t.Fatalf("expected checksum header")
	}
	var signed manifest.SignedRunManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if signed.Manifest.RunID != "run-1" {
		t.Fatalf("unexpected manifest payload: %+v", signed.Manifest)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFetchRunManifest(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		Server:     config.ServerConfig{RunManifestEnabled: true},
		Capability: config.CapabilityConfig{SigningSecret: "secret"},
	}
	handler := NewRunsHandler(cfg, &store.Store{DB: db}, nil, nil, nil)

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("name", []byte(`{}`), "@daily"))

	payload := manifest.RunManifestPayload{Version: manifest.RunManifestVersion, RunID: "run-1", TopicID: "topic", UserID: "user", CreatedAt: time.Unix(0, 0)}
	signed, _ := manifest.SignRunManifest(payload, "secret", time.Unix(0, 0))
	raw, _ := json.Marshal(signed)

	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow("run-1", "topic", raw, signed.Checksum, signed.Signature, signed.Algorithm, signed.SignedAt, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at FROM run_manifests WHERE run_id=$1")).
		WithArgs("run-1").
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/runs/run-1/manifest", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id", "run_id")
	ctx.SetParamValues("topic", "run-1")
	ctx.Set("user_id", "user")

	if err := handler.fetchManifest(ctx); err != nil {
		t.Fatalf("fetchManifest: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Run-Manifest-Signature") == "" {
		t.Fatalf("expected signature header")
	}

	var resp manifest.SignedRunManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Manifest.RunID != "run-1" {
		t.Fatalf("unexpected manifest run id: %+v", resp.Manifest)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
