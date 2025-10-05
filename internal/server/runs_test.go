package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

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
