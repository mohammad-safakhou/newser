package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type budgetStoreStub struct {
	topics       map[string]bool
	getTopicErr  error
	config       budget.Config
	hasConfig    bool
	configErr    error
	pending      store.BudgetApprovalRecord
	hasPending   bool
	pendingErr   error
	upsertErr    error
	savedConfig  budget.Config
	savedTopicID string
}

func (s *budgetStoreStub) GetTopicByID(ctx context.Context, topicID, userID string) (string, []byte, string, error) {
	if s.getTopicErr != nil {
		return "", nil, "", s.getTopicErr
	}
	if s.topics != nil && !s.topics[topicID+"/"+userID] {
		return "", nil, "", errors.New("topic not found")
	}
	return "Topic", nil, userID, nil
}

func (s *budgetStoreStub) UpsertTopicBudgetConfig(ctx context.Context, topicID string, cfg budget.Config) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.savedTopicID = topicID
	s.savedConfig = cfg
	return nil
}

func (s *budgetStoreStub) GetTopicBudgetConfig(ctx context.Context, topicID string) (budget.Config, bool, error) {
	if s.configErr != nil {
		return budget.Config{}, false, s.configErr
	}
	return s.config, s.hasConfig, nil
}

func (s *budgetStoreStub) GetPendingBudgetApproval(ctx context.Context, topicID string) (store.BudgetApprovalRecord, bool, error) {
	if s.pendingErr != nil {
		return store.BudgetApprovalRecord{}, false, s.pendingErr
	}
	return s.pending, s.hasPending, nil
}

func TestBudgetHandlerGetConfigWithPending(t *testing.T) {
	st := &budgetStoreStub{
		topics:    map[string]bool{"topic-1/user-1": true},
		config:    budget.Config{RequireApproval: true},
		hasConfig: true,
		pending: store.BudgetApprovalRecord{
			RunID:         "run-1",
			TopicID:       "topic-1",
			EstimatedCost: 12.5,
			Threshold:     10,
			RequestedBy:   "user-1",
			Status:        "pending",
			CreatedAt:     time.Unix(1700000000, 0),
		},
		hasPending: true,
	}
	h := NewBudgetHandler(st)
	if h == nil {
		t.Fatalf("expected handler")
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic-1/budget", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic-1")
	ctx.Set("user_id", "user-1")

	if err := h.getConfig(ctx); err != nil {
		t.Fatalf("getConfig returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp budgetGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasConfig || resp.Config == nil || !resp.Config.RequireApproval {
		t.Fatalf("unexpected config: %#v", resp)
	}
	if resp.PendingApproval == nil || resp.PendingApproval.RunID != "run-1" {
		t.Fatalf("expected pending approval, got %#v", resp.PendingApproval)
	}
}

func TestBudgetHandlerPutConfigValidation(t *testing.T) {
	st := &budgetStoreStub{topics: map[string]bool{"topic-1/user-1": true}}
	h := NewBudgetHandler(st)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-1/budget", strings.NewReader(`{"max_cost":-5}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic-1")
	ctx.Set("user_id", "user-1")

	err := h.putConfig(ctx)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 http error, got %#v", err)
	}
}

func TestBudgetHandlerPutPersistsConfig(t *testing.T) {
	st := &budgetStoreStub{topics: map[string]bool{"topic-1/user-1": true}}
	h := NewBudgetHandler(st)

	payload := `{"max_cost":50,"approval_threshold":25,"require_approval":true}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/topics/topic-1/budget", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic-1")
	ctx.Set("user_id", "user-1")

	if err := h.putConfig(ctx); err != nil {
		t.Fatalf("putConfig returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if st.savedTopicID != "topic-1" {
		t.Fatalf("expected topic saved, got %q", st.savedTopicID)
	}
	if st.savedConfig.MaxCost == nil || *st.savedConfig.MaxCost != 50 {
		t.Fatalf("expected max cost saved, got %#v", st.savedConfig.MaxCost)
	}
	if !st.savedConfig.RequireApproval {
		t.Fatalf("expected require approval true")
	}
}

func TestBudgetHandlerGetPendingNoContent(t *testing.T) {
	st := &budgetStoreStub{topics: map[string]bool{"topic-1/user-1": true}}
	h := NewBudgetHandler(st)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic-1/budget/pending", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic-1")
	ctx.Set("user_id", "user-1")

	if err := h.getPending(ctx); err != nil {
		t.Fatalf("getPending returned error: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}
