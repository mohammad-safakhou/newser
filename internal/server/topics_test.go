package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func TestUpdatePolicySuccess(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &TopicsHandler{Store: &store.Store{DB: db}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic-123", "user-456").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).
			AddRow("Tech", []byte(`{}`), "@daily"))

	mock.ExpectExec(`INSERT INTO topic_update_policies`).
		WithArgs("topic-123", int64(time.Hour/time.Second), int64(2*time.Hour/time.Second), "adaptive", int64(0), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-123/policy", strings.NewReader(`{"repeat_mode":"adaptive","refresh_interval":"1h","dedup_window":"2h"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.Set("user_id", "user-456")
	ctx.SetParamNames("id")
	ctx.SetParamValues("topic-123")

	if err := handler.updatePolicy(ctx); err != nil {
		t.Fatalf("updatePolicy: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp TemporalPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RepeatMode != "adaptive" || resp.RefreshInterval != "1h0m0s" || resp.DedupWindow != "2h0m0s" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestUpdatePolicyInvalidDuration(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &TopicsHandler{Store: &store.Store{DB: db}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic-123", "user-456").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).
			AddRow("Tech", []byte(`{}`), "@daily"))

	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-123/policy", strings.NewReader(`{"repeat_mode":"adaptive","refresh_interval":"bananas"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.Set("user_id", "user-456")
	ctx.SetParamNames("id")
	ctx.SetParamValues("topic-123")

	err = handler.updatePolicy(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 error, got %#v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetTopicIncludesPolicy(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &TopicsHandler{Store: &store.Store{DB: db}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic-123", "user-456").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).
			AddRow("Tech", []byte(`{"focus":"ai"}`), "@weekly"))

	mock.ExpectQuery(`SELECT refresh_interval_seconds,\s+dedup_window_seconds,\s+repeat_mode,\s+freshness_threshold_seconds,\s+metadata\s+FROM topic_update_policies`).
		WithArgs("topic-123").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_interval_seconds", "dedup_window_seconds", "repeat_mode", "freshness_threshold_seconds", "metadata"}).
			AddRow(int64(1800), int64(3600), "manual", int64(0), []byte(`{"channels":["rss"]}`)))

	mock.ExpectQuery(`SELECT max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata\s+FROM topic_budget_configs`).
		WithArgs("topic-123").
		WillReturnRows(sqlmock.NewRows([]string{"max_cost", "max_tokens", "max_time_seconds", "approval_threshold", "require_approval", "metadata"}).
			AddRow(25.5, int64(90000), int64(7200), 10.0, true, []byte(`{"team":"intel"}`)))

	mock.ExpectQuery(`SELECT run_id, topic_id, estimated_cost`).
		WithArgs("topic-123").
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "estimated_cost", "approval_threshold", "requested_by", "status", "created_at", "decided_at", "decided_by", "reason"}).
			AddRow("run-1", "topic-123", 30.0, 10.0, "user-456", "pending", time.Now(), nil, nil, ""))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic-123", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.Set("user_id", "user-456")
	ctx.SetParamNames("id")
	ctx.SetParamValues("topic-123")

	if err := handler.get(ctx); err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	var resp TopicDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Policy == nil || resp.Policy.RepeatMode != "manual" || resp.Policy.RefreshInterval != "30m0s" {
		t.Fatalf("unexpected policy: %+v", resp.Policy)
	}
	if resp.Budget == nil || resp.Budget.MaxCost == nil || *resp.Budget.MaxCost != 25.5 {
		t.Fatalf("expected budget response, got %+v", resp.Budget)
	}
	if resp.PendingBudget == nil || resp.PendingBudget.RunID != "run-1" {
		t.Fatalf("expected pending budget approval")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestUpdateBudgetSuccess(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &TopicsHandler{Store: &store.Store{DB: db}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic-1", "user-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).
			AddRow("Topic", []byte(`{}`), "@daily"))

	mock.ExpectQuery(`SELECT max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata\s+FROM topic_budget_configs`).
		WithArgs("topic-1").
		WillReturnRows(sqlmock.NewRows([]string{"max_cost", "max_tokens", "max_time_seconds", "approval_threshold", "require_approval", "metadata"}))

	mock.ExpectExec(`INSERT INTO topic_budget_configs`).
		WithArgs("topic-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), true, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-1/budget", strings.NewReader(`{"max_cost": 42.5, "max_tokens": 80000, "require_approval": true}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.Set("user_id", "user-1")
	ctx.SetParamNames("id")
	ctx.SetParamValues("topic-1")

	if err := handler.updateBudget(ctx); err != nil {
		t.Fatalf("updateBudget: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	var resp BudgetConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MaxCost == nil || *resp.MaxCost != 42.5 {
		t.Fatalf("unexpected budget response: %+v", resp)
	}
	if !resp.RequireApproval {
		t.Fatalf("require approval not set")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestUpdateBudgetValidation(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := &TopicsHandler{Store: &store.Store{DB: db}}

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic-1", "user-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).
			AddRow("Topic", []byte(`{}`), "@daily"))

	mock.ExpectQuery(`SELECT max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata\s+FROM topic_budget_configs`).
		WithArgs("topic-1").
		WillReturnRows(sqlmock.NewRows([]string{"max_cost", "max_tokens", "max_time_seconds", "approval_threshold", "require_approval", "metadata"}))

	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-1/budget", strings.NewReader(`{"max_cost": -5}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.Set("user_id", "user-1")
	ctx.SetParamNames("id")
	ctx.SetParamValues("topic-1")

	err = handler.updateBudget(ctx)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 error, got %#v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
