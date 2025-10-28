package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type semanticStore interface {
	SearchRunEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.RunEmbeddingSearchResult, error)
	SearchPlanStepEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.PlanStepEmbeddingSearchResult, error)
}

// MemoryHandler exposes semantic memory search APIs.
type MemoryHandler struct {
	store    semanticStore
	cfg      *config.Config
	provider agentcore.LLMProvider
	manager  memorysvc.Manager
	logger   *log.Logger
	semantic bool
}

type episodicSnapshotPayload struct {
	RunID        string                     `json:"run_id"`
	TopicID      string                     `json:"topic_id"`
	UserID       string                     `json:"user_id"`
	Thought      agentcore.UserThought      `json:"thought"`
	PlanDocument *planner.PlanDocument      `json:"plan_document"`
	PlanRaw      []byte                     `json:"plan_raw"`
	PlanPrompt   string                     `json:"plan_prompt"`
	Result       agentcore.ProcessingResult `json:"result"`
	Steps        []agentcore.EpisodicStep   `json:"steps"`
}

func NewMemoryHandler(cfg *config.Config, st semanticStore, provider agentcore.LLMProvider, manager memorysvc.Manager, logger *log.Logger) *MemoryHandler {
	if cfg == nil {
		return nil
	}
	semanticEnabled := cfg.Memory.Semantic.Enabled && st != nil && provider != nil
	if !semanticEnabled && manager == nil {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[MEMORY] ", log.LstdFlags)
	}
	return &MemoryHandler{store: st, cfg: cfg, provider: provider, manager: manager, logger: logger, semantic: semanticEnabled}
}

func (h *MemoryHandler) Register(g *echo.Group, secret []byte) {
	if h == nil {
		return
	}
	g.Use(runtime.EchoAuthMiddleware(secret))
	if h.semantic {
		g.POST("/search", h.search)
	}
	if h.manager != nil {
		g.POST("/write", h.write, runtime.RequireScopes("memory:write"))
		g.POST("/summarize", h.summarize, runtime.RequireScopes("memory:summarize"))
		g.POST("/delta", h.delta, runtime.RequireScopes("memory:delta"))
		g.GET("/health", h.health, runtime.RequireScopes("memory:read"))
		g.GET("/templates", h.listTemplates, runtime.RequireScopes("memory:read"))
		g.GET("/templates/fingerprints", h.listFingerprints, runtime.RequireScopes("memory:read"))
		g.POST("/templates/promote", h.promoteTemplate, runtime.RequireScopes("memory:write"))
		g.POST("/templates/approve", h.approveTemplate, runtime.RequireScopes("memory:write"))
	}
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
	if h == nil || !h.semantic || h.store == nil || h.provider == nil {
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

func (h *MemoryHandler) write(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	var payload episodicSnapshotPayload
	if err := c.Bind(&payload); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	snapshot := agentcore.EpisodicSnapshot{
		RunID:        payload.RunID,
		TopicID:      payload.TopicID,
		UserID:       payload.UserID,
		Thought:      payload.Thought,
		PlanDocument: payload.PlanDocument,
		PlanRaw:      payload.PlanRaw,
		PlanPrompt:   payload.PlanPrompt,
		Result:       payload.Result,
		Steps:        payload.Steps,
	}
	if snapshot.UserID == "" {
		if userID, ok := c.Get("user_id").(string); ok && userID != "" {
			snapshot.UserID = userID
		}
	}
	if err := h.manager.WriteEpisode(c.Request().Context(), snapshot); err != nil {
		return h.toHTTPError(err)
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *MemoryHandler) summarize(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	var req memorysvc.SummaryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.TopicID == "" {
		req.TopicID = c.QueryParam("topic_id")
	}
	resp, err := h.manager.Summarize(c.Request().Context(), req)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *MemoryHandler) delta(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	var req memorysvc.DeltaRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.TopicID == "" {
		req.TopicID = c.QueryParam("topic_id")
	}
	resp, err := h.manager.Delta(c.Request().Context(), req)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *MemoryHandler) health(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	stats, err := h.manager.Health(c.Request().Context())
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, stats)
}

func (h *MemoryHandler) listTemplates(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	topicID := strings.TrimSpace(c.QueryParam("topic_id"))
	templates, err := h.manager.ListTemplates(c.Request().Context(), topicID)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, templates)
}

func (h *MemoryHandler) listFingerprints(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	topicID := strings.TrimSpace(c.QueryParam("topic_id"))
	limit := 20
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "limit must be a positive integer")
		}
		limit = parsed
	}
	fingerprints, err := h.manager.ListFingerprints(c.Request().Context(), topicID, limit)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, fingerprints)
}

func (h *MemoryHandler) promoteTemplate(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	var req memorysvc.TemplatePromotionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.TopicID == "" {
		req.TopicID = strings.TrimSpace(c.QueryParam("topic_id"))
	}
	tpl, err := h.manager.PromoteFingerprint(c.Request().Context(), req)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusCreated, tpl)
}

func (h *MemoryHandler) approveTemplate(c echo.Context) error {
	if h.manager == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "memory manager disabled")
	}
	var req memorysvc.TemplateApprovalRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	tpl, err := h.manager.ApproveTemplate(c.Request().Context(), req)
	if err != nil {
		return h.toHTTPError(err)
	}
	return c.JSON(http.StatusOK, tpl)
}

func (h *MemoryHandler) toHTTPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return echo.NewHTTPError(http.StatusRequestTimeout, "request cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return echo.NewHTTPError(http.StatusRequestTimeout, "request timeout")
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "required"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.Contains(msg, "unavailable"):
		return echo.NewHTTPError(http.StatusServiceUnavailable, msg)
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, msg)
	}
}
