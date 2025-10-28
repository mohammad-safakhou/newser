package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type stubSemanticStore struct {
	runResults    []store.RunEmbeddingSearchResult
	stepResults   []store.PlanStepEmbeddingSearchResult
	runErr        error
	stepErr       error
	lastTopicID   string
	lastVector    []float32
	lastTopK      int
	lastThreshold float64
	lastStepsTopK int
}

func (s *stubSemanticStore) SearchRunEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.RunEmbeddingSearchResult, error) {
	s.lastTopicID = topicID
	s.lastVector = append([]float32(nil), vector...)
	s.lastTopK = topK
	s.lastThreshold = threshold
	return s.runResults, s.runErr
}

func (s *stubSemanticStore) SearchPlanStepEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.PlanStepEmbeddingSearchResult, error) {
	s.lastStepsTopK = topK
	return s.stepResults, s.stepErr
}

type stubLLMProvider struct {
	embedVectors [][]float32
	embedErr     error
	lastInputs   []string
}

func (p *stubLLMProvider) Generate(context.Context, string, string, map[string]interface{}) (string, error) {
	return "", errors.New("not implemented")
}

func (p *stubLLMProvider) GenerateWithTokens(context.Context, string, string, map[string]interface{}) (string, int64, int64, error) {
	return "", 0, 0, errors.New("not implemented")
}

func (p *stubLLMProvider) Embed(ctx context.Context, model string, input []string) ([][]float32, error) {
	p.lastInputs = append([]string(nil), input...)
	return p.embedVectors, p.embedErr
}

func (p *stubLLMProvider) GetAvailableModels() []string { return nil }

func (p *stubLLMProvider) GetModelInfo(string) (core.ModelInfo, error) {
	return core.ModelInfo{}, errors.New("not implemented")
}

func (p *stubLLMProvider) CalculateCost(int64, int64, string) float64 { return 0 }

type stubMemoryManager struct {
	lastSnapshot core.EpisodicSnapshot
	writeErr     error
	summaryReq   memorysvc.SummaryRequest
	summaryResp  memorysvc.SummaryResponse
	summaryErr   error
	deltaReq     memorysvc.DeltaRequest
	deltaResp    memorysvc.DeltaResponse
	deltaErr     error
	healthResp   memorysvc.HealthStats
	healthErr    error
	fingerprints []core.TemplateFingerprintState
	templates    []core.ProceduralTemplate
	promoteReq   memorysvc.TemplatePromotionRequest
	promoteResp  core.ProceduralTemplate
	promoteErr   error
	approveReq   memorysvc.TemplateApprovalRequest
	approveResp  core.ProceduralTemplate
	approveErr   error
}

func (m *stubMemoryManager) WriteEpisode(ctx context.Context, snapshot core.EpisodicSnapshot) error {
	if ctx == nil {
		return errors.New("missing context")
	}
	m.lastSnapshot = snapshot
	return m.writeErr
}

func (m *stubMemoryManager) Summarize(ctx context.Context, req memorysvc.SummaryRequest) (memorysvc.SummaryResponse, error) {
	if ctx == nil {
		return memorysvc.SummaryResponse{}, errors.New("missing context")
	}
	m.summaryReq = req
	return m.summaryResp, m.summaryErr
}

func (m *stubMemoryManager) Delta(ctx context.Context, req memorysvc.DeltaRequest) (memorysvc.DeltaResponse, error) {
	if ctx == nil {
		return memorysvc.DeltaResponse{}, errors.New("missing context")
	}
	m.deltaReq = req
	return m.deltaResp, m.deltaErr
}

func (m *stubMemoryManager) Health(ctx context.Context) (memorysvc.HealthStats, error) {
	if ctx == nil {
		return memorysvc.HealthStats{}, errors.New("missing context")
	}
	return m.healthResp, m.healthErr
}

func (m *stubMemoryManager) ListFingerprints(ctx context.Context, topicID string, limit int) ([]core.TemplateFingerprintState, error) {
	if ctx == nil {
		return nil, errors.New("missing context")
	}
	return m.fingerprints, nil
}

func (m *stubMemoryManager) PromoteFingerprint(ctx context.Context, req memorysvc.TemplatePromotionRequest) (core.ProceduralTemplate, error) {
	if ctx == nil {
		return core.ProceduralTemplate{}, errors.New("missing context")
	}
	m.promoteReq = req
	if m.promoteErr != nil {
		return core.ProceduralTemplate{}, m.promoteErr
	}
	return m.promoteResp, nil
}

func (m *stubMemoryManager) ListTemplates(ctx context.Context, topicID string) ([]core.ProceduralTemplate, error) {
	if ctx == nil {
		return nil, errors.New("missing context")
	}
	return m.templates, nil
}

func (m *stubMemoryManager) ApproveTemplate(ctx context.Context, req memorysvc.TemplateApprovalRequest) (core.ProceduralTemplate, error) {
	if ctx == nil {
		return core.ProceduralTemplate{}, errors.New("missing context")
	}
	m.approveReq = req
	if m.approveErr != nil {
		return core.ProceduralTemplate{}, m.approveErr
	}
	return m.approveResp, nil
}

func TestMemoryHandlerSearchSuccess(t *testing.T) {
	storeStub := &stubSemanticStore{
		runResults: []store.RunEmbeddingSearchResult{
			{RunID: "run-1", TopicID: "topic-42", Kind: "summary", Distance: 0.2, Metadata: map[string]interface{}{"title": "Example"}, CreatedAt: time.Unix(1700000000, 0)},
		},
		stepResults: []store.PlanStepEmbeddingSearchResult{
			{RunID: "run-1", TopicID: "topic-42", TaskID: "task-7", Kind: "plan", Distance: 1.4, Metadata: map[string]interface{}{"node": "step"}, CreatedAt: time.Unix(1700000500, 0)},
		},
	}
	providerStub := &stubLLMProvider{embedVectors: [][]float32{{0.1, 0.2, 0.3}}}
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{
		Enabled:         true,
		EmbeddingModel:  "text-embedding",
		SearchTopK:      5,
		SearchThreshold: 0.5,
	}}}
	h := NewMemoryHandler(cfg, storeStub, providerStub, nil, nil)
	if h == nil {
		t.Fatalf("expected handler")
	}

	payload := `{"query":"climate update","topic_id":"topic-42","top_k":2,"threshold":0.3,"include_steps":true}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/memory/search", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := h.search(ctx); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp semanticSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Query != "climate update" {
		t.Fatalf("expected query echoed, got %q", resp.Query)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if got := resp.Results[0].Similarity; got != clampSimilarity(1-0.2) {
		t.Fatalf("unexpected similarity: %v", got)
	}
	if len(resp.PlanStepResults) != 1 {
		t.Fatalf("expected plan step results")
	}
	if storeStub.lastTopK != 2 {
		t.Fatalf("expected topK override to 2, got %d", storeStub.lastTopK)
	}
	if storeStub.lastThreshold != 0.3 {
		t.Fatalf("expected threshold 0.3, got %v", storeStub.lastThreshold)
	}
	if len(providerStub.lastInputs) != 1 || providerStub.lastInputs[0] != "climate update" {
		t.Fatalf("unexpected embed inputs: %#v", providerStub.lastInputs)
	}
	if storeStub.lastStepsTopK == 0 {
		t.Fatalf("expected plan step search invoked")
	}
}

func TestMemoryHandlerRequiresQuery(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: true}}}
	h := NewMemoryHandler(cfg, &stubSemanticStore{}, &stubLLMProvider{embedVectors: [][]float32{{1}}}, nil, nil)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/memory/search", strings.NewReader(`{"query":"   "}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := h.search(ctx)
	if err == nil {
		t.Fatalf("expected error for blank query")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 http error, got %#v", err)
	}
}

func TestNewMemoryHandlerDisabled(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}}}
	h := NewMemoryHandler(cfg, &stubSemanticStore{}, &stubLLMProvider{}, nil, nil)
	if h != nil {
		t.Fatalf("expected nil handler when semantic memory disabled")
	}
}

func TestNewMemoryHandlerWithManagerOnly(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}, Episodic: config.EpisodicMemoryConfig{Enabled: true}}}
	manager := &stubMemoryManager{}
	h := NewMemoryHandler(cfg, nil, nil, manager, nil)
	if h == nil {
		t.Fatalf("expected handler when manager present")
	}
}

func TestMemoryHandlerWriteSuccess(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}, Episodic: config.EpisodicMemoryConfig{Enabled: true}}}
	manager := &stubMemoryManager{}
	h := NewMemoryHandler(cfg, nil, nil, manager, nil)
	if h == nil {
		t.Fatalf("expected handler")
	}
	secret := []byte("secret")
	e := echo.New()
	group := e.Group("/memory")
	h.Register(group, secret)
	token, err := runtime.SignJWT("user-1", secret, time.Hour, "memory:write")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	payload := `{"run_id":"run-1","topic_id":"topic-42","user_id":"user-1"}`
	req := httptest.NewRequest(http.MethodPost, "/memory/write", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if manager.lastSnapshot.RunID != "run-1" {
		t.Fatalf("snapshot not forwarded")
	}
}

func TestMemoryHandlerWriteRequiresScope(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}, Episodic: config.EpisodicMemoryConfig{Enabled: true}}}
	manager := &stubMemoryManager{}
	h := NewMemoryHandler(cfg, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, err := runtime.SignJWT("user-1", secret, time.Hour)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/memory/write", strings.NewReader(`{"run_id":"run-1","topic_id":"topic-42","user_id":"user-1"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing scope, got %d", rec.Code)
	}
}

func TestMemoryHandlerSummarize(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}, Episodic: config.EpisodicMemoryConfig{Enabled: true}}}
	manager := &stubMemoryManager{
		summaryResp: memorysvc.SummaryResponse{TopicID: "topic-42", Summary: "- latest", GeneratedAt: time.Unix(170000, 0)},
	}
	h := NewMemoryHandler(cfg, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:summarize")
	req := httptest.NewRequest(http.MethodPost, "/memory/summarize", strings.NewReader(`{"topic_id":"topic-42","max_runs":3}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp memorysvc.SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TopicID != "topic-42" {
		t.Fatalf("unexpected topic: %s", resp.TopicID)
	}
	if manager.summaryReq.MaxRuns != 3 {
		t.Fatalf("expected max_runs forwarded")
	}
}

func TestMemoryHandlerDelta(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Semantic: config.SemanticMemoryConfig{Enabled: false}, Episodic: config.EpisodicMemoryConfig{Enabled: true}}}
	manager := &stubMemoryManager{
		deltaResp: memorysvc.DeltaResponse{TopicID: "topic-42", Novel: []memorysvc.DeltaItem{{ID: "a"}}},
	}
	h := NewMemoryHandler(cfg, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:delta")
	req := httptest.NewRequest(http.MethodPost, "/memory/delta", strings.NewReader(`{"topic_id":"topic-42","items":[{"id":"a"}]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if manager.deltaReq.TopicID != "topic-42" {
		t.Fatalf("delta request topic not forwarded")
	}
}

func TestMemoryHandlerListTemplates(t *testing.T) {
	manager := &stubMemoryManager{templates: []core.ProceduralTemplate{{ID: "tpl-1", Name: "Template"}}}
	h := NewMemoryHandler(&config.Config{}, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:read")
	req := httptest.NewRequest(http.MethodGet, "/memory/templates", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMemoryHandlerListFingerprints(t *testing.T) {
	manager := &stubMemoryManager{fingerprints: []core.TemplateFingerprintState{{TopicID: "topic", Fingerprint: "abc", Occurrences: 3}}}
	h := NewMemoryHandler(&config.Config{}, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:read")
	req := httptest.NewRequest(http.MethodGet, "/memory/templates/fingerprints?limit=5", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMemoryHandlerPromoteTemplate(t *testing.T) {
	manager := &stubMemoryManager{promoteResp: core.ProceduralTemplate{ID: "tpl-1"}}
	h := NewMemoryHandler(&config.Config{}, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:write")
	req := httptest.NewRequest(http.MethodPost, "/memory/templates/promote", strings.NewReader(`{"topic_id":"topic","fingerprint":"abc"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if manager.promoteReq.Fingerprint != "abc" {
		t.Fatalf("promotion request not recorded")
	}
}

func TestMemoryHandlerApproveTemplate(t *testing.T) {
	manager := &stubMemoryManager{approveResp: core.ProceduralTemplate{ID: "tpl-1"}}
	h := NewMemoryHandler(&config.Config{}, nil, nil, manager, nil)
	secret := []byte("secret")
	e := echo.New()
	h.Register(e.Group("/memory"), secret)
	token, _ := runtime.SignJWT("svc", secret, time.Hour, "memory:write")
	req := httptest.NewRequest(http.MethodPost, "/memory/templates/approve", strings.NewReader(`{"template_id":"tpl-1"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if manager.approveReq.TemplateID != "tpl-1" {
		t.Fatalf("approval request not recorded")
	}
}

func TestMemoryHandlerHealth(t *testing.T) {
	manager := &stubMemoryManager{healthResp: memorysvc.HealthStats{Episodes: 12, CollectedAt: time.Unix(1700, 0).UTC().Format(time.RFC3339)}}
	cfg := &config.Config{}
	h := NewMemoryHandler(cfg, nil, nil, manager, log.New(io.Discard, "", 0))
	if h == nil {
		t.Fatalf("expected handler")
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/memory/health", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := h.health(ctx); err != nil {
		t.Fatalf("health returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp memorysvc.HealthStats
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Episodes != 12 {
		t.Fatalf("unexpected episodes: %d", resp.Episodes)
	}
}
