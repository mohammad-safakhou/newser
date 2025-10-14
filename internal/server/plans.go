package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/runtime"
)

const (
	PlanEstimateModeAuto     = "auto"
	PlanEstimateModeDocument = "document"
	PlanEstimateModeTaskSum  = "task-sum"
	PlanEstimateModeNone     = "none"
)

// PlansHandler exposes plan validation utilities (dry-run).
type PlansHandler struct {
	Repo         agentcore.PlanRepository
	EstimateMode string
	EnableDryRun bool
}

func NewPlansHandler(repo agentcore.PlanRepository, enableDryRun bool, estimateMode string) *PlansHandler {
	if estimateMode == "" {
		estimateMode = PlanEstimateModeAuto
	}
	return &PlansHandler{Repo: repo, EstimateMode: estimateMode, EnableDryRun: enableDryRun}
}

func (h *PlansHandler) Register(g *echo.Group, secret []byte) {
	g.Use(runtime.EchoAuthMiddleware(secret))
	if h.EnableDryRun {
		g.POST("/dry-run", h.dryRun)
	}
	g.GET(":thought_id", h.getLatest)
}

type planDryRunRequest struct {
	ThoughtID string          `json:"thought_id"`
	Plan      json.RawMessage `json:"plan"`
}

type planDryRunResponse struct {
	Valid         bool    `json:"valid"`
	PlanID        string  `json:"plan_id,omitempty"`
	TaskCount     int     `json:"task_count"`
	EstimatedCost float64 `json:"estimated_cost"`
	EstimatedTime string  `json:"estimated_time"`
	Confidence    float64 `json:"confidence"`
	Message       string  `json:"message,omitempty"`
}

type planGetResponse struct {
	PlanID    string                 `json:"plan_id"`
	ThoughtID string                 `json:"thought_id"`
	Plan      *planner.PlanDocument  `json:"plan"`
	Raw       map[string]interface{} `json:"raw"`
	UpdatedAt time.Time              `json:"updated_at"`
}

func (h *PlansHandler) dryRun(c echo.Context) error {
	if !h.EnableDryRun {
		return echo.NewHTTPError(http.StatusNotFound, "plan dry-run disabled")
	}

	var req planDryRunRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if len(req.Plan) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "plan payload is required")
	}
	doc, normalized, err := planner.NormalizePlanDocument(req.Plan)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Derive metrics
	taskCount := len(doc.Tasks)
	estimatedCost, estimatedTime := h.estimatePlan(doc)
	confidence := doc.Confidence

	planID := doc.PlanID
	if h.Repo != nil {
		id, err := h.Repo.SavePlanGraph(c.Request().Context(), req.ThoughtID, doc, normalized)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		planID = id
	}

	resp := planDryRunResponse{
		Valid:         true,
		PlanID:        planID,
		TaskCount:     taskCount,
		EstimatedCost: estimatedCost,
		EstimatedTime: estimatedTime,
		Confidence:    confidence,
		Message:       "plan validated",
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *PlansHandler) estimatePlan(doc *planner.PlanDocument) (float64, string) {
	mode := h.EstimateMode
	if mode == "" {
		mode = PlanEstimateModeAuto
	}
	switch mode {
	case PlanEstimateModeNone:
		return 0, ""
	case PlanEstimateModeDocument:
		if doc.Estimates != nil {
			return doc.Estimates.TotalCost, doc.Estimates.TotalTime
		}
		return h.sumTaskEstimates(doc), ""
	case PlanEstimateModeTaskSum:
		return h.sumTaskEstimates(doc), ""
	case PlanEstimateModeAuto:
		fallthrough
	default:
		if doc.Estimates != nil {
			return doc.Estimates.TotalCost, doc.Estimates.TotalTime
		}
		return h.sumTaskEstimates(doc), ""
	}
}

func (h *PlansHandler) sumTaskEstimates(doc *planner.PlanDocument) float64 {
	sum := 0.0
	for _, t := range doc.Tasks {
		sum += t.EstimatedCost
	}
	return sum
}

func (h *PlansHandler) getLatest(c echo.Context) error {
	if h.Repo == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "plan repository unavailable")
	}
	thoughtID := c.Param("thought_id")
	if thoughtID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "thought_id required")
	}
	stored, ok, err := h.Repo.GetLatestPlanGraph(c.Request().Context(), thoughtID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "plan not found")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(stored.RawJSON, &raw); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	resp := planGetResponse{
		PlanID:    stored.PlanID,
		ThoughtID: stored.ThoughtID,
		Plan:      stored.Document,
		Raw:       raw,
		UpdatedAt: stored.UpdatedAt,
	}
	return c.JSON(http.StatusOK, resp)
}
