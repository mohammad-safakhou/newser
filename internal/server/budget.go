package server

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type budgetStore interface {
	GetTopicByID(ctx context.Context, topicID, userID string) (string, []byte, string, error)
	UpsertTopicBudgetConfig(ctx context.Context, topicID string, cfg budget.Config) error
	GetTopicBudgetConfig(ctx context.Context, topicID string) (budget.Config, bool, error)
	GetPendingBudgetApproval(ctx context.Context, topicID string) (store.BudgetApprovalRecord, bool, error)
}

type BudgetHandler struct {
	store budgetStore
}

func NewBudgetHandler(st budgetStore) *BudgetHandler {
	if st == nil {
		return nil
	}
	return &BudgetHandler{store: st}
}

func (h *BudgetHandler) Register(g *echo.Group, secret []byte) {
	if h == nil {
		return
	}
	group := g.Group("/:topic_id/budget")
	group.Use(runtime.EchoAuthMiddleware(secret))
	group.GET("", h.getConfig)
	group.PUT("", h.putConfig)
	group.GET("/pending", h.getPending)
}

type budgetConfigPayload struct {
	MaxCost           *float64               `json:"max_cost,omitempty"`
	MaxTokens         *int64                 `json:"max_tokens,omitempty"`
	MaxTimeSeconds    *int64                 `json:"max_time_seconds,omitempty"`
	ApprovalThreshold *float64               `json:"approval_threshold,omitempty"`
	RequireApproval   bool                   `json:"require_approval"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

type budgetGetResponse struct {
	HasConfig       bool                   `json:"has_config"`
	Config          *budgetConfigPayload   `json:"config,omitempty"`
	PendingApproval *budgetApprovalPayload `json:"pending_approval,omitempty"`
}

type budgetApprovalPayload struct {
	RunID         string    `json:"run_id"`
	EstimatedCost float64   `json:"estimated_cost"`
	Threshold     float64   `json:"threshold"`
	RequestedBy   string    `json:"requested_by"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

func (h *BudgetHandler) getConfig(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	cfg, ok, err := h.store.GetTopicBudgetConfig(ctx, topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	pending, hasPending, err := h.store.GetPendingBudgetApproval(ctx, topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	resp := budgetGetResponse{HasConfig: ok}
	if ok {
		resp.Config = budgetConfigToPayload(cfg)
	}
	if hasPending {
		resp.PendingApproval = &budgetApprovalPayload{
			RunID:         pending.RunID,
			EstimatedCost: pending.EstimatedCost,
			Threshold:     pending.Threshold,
			RequestedBy:   pending.RequestedBy,
			Status:        pending.Status,
			CreatedAt:     pending.CreatedAt,
		}
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *BudgetHandler) putConfig(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	var payload budgetConfigPayload
	if err := c.Bind(&payload); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	cfg := payload.ToConfig()
	if err := cfg.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := h.store.UpsertTopicBudgetConfig(ctx, topicID, cfg); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, budgetConfigToPayload(cfg))
}

func (h *BudgetHandler) getPending(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	pending, ok, err := h.store.GetPendingBudgetApproval(ctx, topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	resp := budgetApprovalPayload{
		RunID:         pending.RunID,
		EstimatedCost: pending.EstimatedCost,
		Threshold:     pending.Threshold,
		RequestedBy:   pending.RequestedBy,
		Status:        pending.Status,
		CreatedAt:     pending.CreatedAt,
	}
	return c.JSON(http.StatusOK, resp)
}

func budgetConfigToPayload(cfg budget.Config) *budgetConfigPayload {
	return &budgetConfigPayload{
		MaxCost:           cfg.MaxCost,
		MaxTokens:         cfg.MaxTokens,
		MaxTimeSeconds:    cfg.MaxTimeSeconds,
		ApprovalThreshold: cfg.ApprovalThreshold,
		RequireApproval:   cfg.RequireApproval,
		Metadata:          cfg.Metadata,
	}
}

func (p budgetConfigPayload) ToConfig() budget.Config {
	cfg := budget.Config{
		Metadata:        nil,
		RequireApproval: p.RequireApproval,
	}
	if p.MaxCost != nil {
		v := *p.MaxCost
		cfg.MaxCost = &v
	}
	if p.MaxTokens != nil {
		v := *p.MaxTokens
		cfg.MaxTokens = &v
	}
	if p.MaxTimeSeconds != nil {
		v := *p.MaxTimeSeconds
		cfg.MaxTimeSeconds = &v
	}
	if p.ApprovalThreshold != nil {
		v := *p.ApprovalThreshold
		cfg.ApprovalThreshold = &v
	}
	if p.Metadata != nil {
		cfg.Metadata = make(map[string]interface{}, len(p.Metadata))
		for k, v := range p.Metadata {
			cfg.Metadata[k] = v
		}
	}
	return cfg
}
