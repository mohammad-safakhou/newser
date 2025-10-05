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
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
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
	h := NewMemoryHandler(cfg, storeStub, providerStub, nil)
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
	h := NewMemoryHandler(cfg, &stubSemanticStore{}, &stubLLMProvider{embedVectors: [][]float32{{1}}}, nil)
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
	h := NewMemoryHandler(cfg, &stubSemanticStore{}, &stubLLMProvider{}, nil)
	if h != nil {
		t.Fatalf("expected nil handler when semantic memory disabled")
	}
}
