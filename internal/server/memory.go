package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// MemoryHandler exposes semantic memory search APIs.
type MemoryHandler struct {
	store    *store.Store
	cfg      *config.Config
	provider agentcore.LLMProvider
	logger   *log.Logger
}

func NewMemoryHandler(cfg *config.Config, st *store.Store, provider agentcore.LLMProvider, logger *log.Logger) *MemoryHandler {
	if cfg == nil || !cfg.Memory.Semantic.Enabled {
		return nil
	}
	if provider == nil {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[MEMORY] ", log.LstdFlags)
	}
	return &MemoryHandler{store: st, cfg: cfg, provider: provider, logger: logger}
}

func (h *MemoryHandler) Register(g *echo.Group, secret []byte) {
	if h == nil {
		return
	}
	g.Use(runtime.EchoAuthMiddleware(secret))
	g.POST("/search", h.search)
}

type semanticSearchRequest struct {
	Query        string   `json:"query"`
	TopicID      string   `json:"topic_id,omitempty"`
	TopK         *int     `json:"top_k,omitempty"`
	Threshold    *float64 `json:"threshold,omitempty"`
	IncludeSteps bool     `json:"include_steps,omitempty"`
}

type semanticSearchResult struct {
	RunID      string                 `json:"run_id"`
	TopicID    string                 `json:"topic_id"`
	Kind       string                 `json:"kind"`
	Distance   float64                `json:"distance"`
	Similarity float64                `json:"similarity"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

type semanticPlanStepResult struct {
	RunID      string                 `json:"run_id"`
	TopicID    string                 `json:"topic_id"`
	TaskID     string                 `json:"task_id"`
	Kind       string                 `json:"kind"`
	Distance   float64                `json:"distance"`
	Similarity float64                `json:"similarity"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

type semanticSearchResponse struct {
	Query           string                   `json:"query"`
	Results         []semanticSearchResult   `json:"results"`
	PlanStepResults []semanticPlanStepResult `json:"plan_step_results,omitempty"`
}

func (h *MemoryHandler) search(c echo.Context) error {
	if h == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "semantic memory disabled")
	}
	var req semanticSearchRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query is required")
	}
	topK := h.cfg.Memory.Semantic.SearchTopK
	if topK <= 0 {
		topK = 5
	}
	if req.TopK != nil && *req.TopK > 0 {
		topK = *req.TopK
	}
	threshold := h.cfg.Memory.Semantic.SearchThreshold
	if req.Threshold != nil && *req.Threshold > 0 {
		threshold = *req.Threshold
	}

	embed, err := h.provider.Embed(c.Request().Context(), h.cfg.Memory.Semantic.EmbeddingModel, []string{req.Query})
	if err != nil {
		h.logger.Printf("semantic search embed failed: %v", err)
		return echo.NewHTTPError(http.StatusBadGateway, "failed to embed query")
	}
	if len(embed) == 0 {
		return c.JSON(http.StatusOK, semanticSearchResponse{Query: req.Query})
	}
	vector := embed[0]

	runs, err := h.store.SearchRunEmbeddings(c.Request().Context(), req.TopicID, vector, topK, threshold)
	if err != nil {
		h.logger.Printf("semantic search query failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "semantic search failed")
	}
	resp := semanticSearchResponse{Query: req.Query}
	for _, hit := range runs {
		resp.Results = append(resp.Results, semanticSearchResult{
			RunID:      hit.RunID,
			TopicID:    hit.TopicID,
			Kind:       hit.Kind,
			Distance:   hit.Distance,
			Similarity: clampSimilarity(1 - hit.Distance),
			Metadata:   hit.Metadata,
			CreatedAt:  hit.CreatedAt,
		})
	}

	if req.IncludeSteps {
		steps, err := h.store.SearchPlanStepEmbeddings(c.Request().Context(), req.TopicID, vector, topK, threshold)
		if err != nil {
			h.logger.Printf("semantic plan-step search failed: %v", err)
		} else {
			for _, hit := range steps {
				resp.PlanStepResults = append(resp.PlanStepResults, semanticPlanStepResult{
					RunID:      hit.RunID,
					TopicID:    hit.TopicID,
					TaskID:     hit.TaskID,
					Kind:       hit.Kind,
					Distance:   hit.Distance,
					Similarity: clampSimilarity(1 - hit.Distance),
					Metadata:   hit.Metadata,
					CreatedAt:  hit.CreatedAt,
				})
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
}

func clampSimilarity(val float64) float64 {
	if val < 0 {
		return 0
	}
	if val > 1 {
		return 1
	}
	return val
}
