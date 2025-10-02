package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
)

type stubPlanRepo struct {
	thought string
	doc     *planner.PlanDocument
	raw     []byte
	planID  string
	saveErr error
	savedAt time.Time

	stored agentcore.StoredPlanGraph
	found  bool
	getErr error
}

func (s *stubPlanRepo) SavePlanGraph(ctx context.Context, thoughtID string, doc *planner.PlanDocument, raw []byte) (string, error) {
	s.thought = thoughtID
	b, _ := json.Marshal(doc)
	var copyDoc planner.PlanDocument
	_ = json.Unmarshal(b, &copyDoc)
	s.doc = &copyDoc
	s.raw = append([]byte(nil), raw...)
	if s.planID == "" {
		s.planID = "plan-test"
	}
	s.savedAt = time.Now()
	s.stored = agentcore.StoredPlanGraph{
		PlanID:    s.planID,
		ThoughtID: thoughtID,
		Document:  &copyDoc,
		RawJSON:   s.raw,
		UpdatedAt: s.savedAt,
	}
	s.found = true
	if s.saveErr != nil {
		return s.planID, s.saveErr
	}
	return s.planID, nil
}

func (s *stubPlanRepo) GetLatestPlanGraph(ctx context.Context, thoughtID string) (agentcore.StoredPlanGraph, bool, error) {
	if s.getErr != nil {
		return agentcore.StoredPlanGraph{}, false, s.getErr
	}
	if !s.found || s.stored.ThoughtID != thoughtID {
		return agentcore.StoredPlanGraph{}, false, nil
	}
	return s.stored, true, nil
}

func TestPlansHandlerDryRunSuccess(t *testing.T) {
	repo := &stubPlanRepo{planID: "plan-xyz"}
	handler := NewPlansHandler(repo)

	payload := `{"thought_id":"thought-1","plan":{"version":"v1","tasks":[{"id":"t1","type":"research"}],"estimates":{"total_cost":3.5,"total_time":"9m"}}}`

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/plans/dry-run", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := handler.dryRun(ctx); err != nil {
		t.Fatalf("dryRun returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp planDryRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response decode: %v", err)
	}
	if !resp.Valid || resp.PlanID != "plan-xyz" || resp.TaskCount != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if repo.doc == nil || repo.doc.Version != "v1" {
		t.Fatalf("expected doc saved")
	}
	if repo.thought != "thought-1" {
		t.Fatalf("expected thought id persisted")
	}
}

func TestPlansHandlerDryRunRejectsInvalidPlan(t *testing.T) {
	handler := NewPlansHandler(nil)
	payload := `{"plan":{"version":"v1"}}`

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/plans/dry-run", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := handler.dryRun(ctx)
	if err == nil {
		t.Fatalf("expected error for invalid plan")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 http error, got %#v", err)
	}
}

func TestPlansHandlerDryRunPropagatesSaveError(t *testing.T) {
	repo := &stubPlanRepo{saveErr: context.DeadlineExceeded}
	handler := NewPlansHandler(repo)

	payload := `{"thought_id":"thought-1","plan":{"version":"v1","tasks":[{"id":"t1","type":"research"}]}}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/plans/dry-run", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := handler.dryRun(ctx)
	if err == nil {
		t.Fatalf("expected error from save")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 http error, got %#v", err)
	}
}

func TestPlansHandlerGetLatest(t *testing.T) {
	repo := &stubPlanRepo{}
	repo.planID = "plan-1"
	repo.stored = agentcore.StoredPlanGraph{
		PlanID:    "plan-1",
		ThoughtID: "thought-1",
		Document:  &planner.PlanDocument{Version: "v1", Tasks: []planner.PlanTask{{ID: "t1", Type: "research"}}},
		RawJSON:   []byte(`{"version":"v1","tasks":[{"id":"t1","type":"research"}]}`),
		UpdatedAt: time.Unix(1700000000, 0),
	}
	repo.found = true
	handler := NewPlansHandler(repo)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/plans/thought-1", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("thought_id")
	ctx.SetParamValues("thought-1")

	if err := handler.getLatest(ctx); err != nil {
		t.Fatalf("getLatest: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp planGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PlanID != "plan-1" || resp.ThoughtID != "thought-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

var _ agentcore.PlanRepository = (*stubPlanRepo)(nil)
