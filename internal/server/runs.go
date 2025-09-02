package server

import (
	"context"
	"net/http"
	"time"

	"log"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type RunsHandler struct {
	store *store.Store
	orch  *core.Orchestrator
	cfg   *config.Config
}

func NewRunsHandler(cfg *config.Config, store *store.Store, orch *core.Orchestrator) *RunsHandler {
	return &RunsHandler{
		store: store,
		orch:  orch,
		cfg:   cfg,
	}
}

func (h *RunsHandler) Register(g *echo.Group, secret []byte) {
	g.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, secret) })
	g.POST("/:topic_id/trigger", h.trigger)
	g.GET("/:topic_id/runs", h.list)
	g.GET("/:topic_id/latest_result", h.latest)
}

func (h *RunsHandler) trigger(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	name, prefsB, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	// create run
	runID, err := h.store.CreateRun(c.Request().Context(), topicID, "running")
	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}

	// launch background processing (use injected orchestrator)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		orch := h.orch
		if orch == nil {
			// Fallback: create once if not provided (should not happen)
			tele := telemetry.NewTelemetry(h.cfg.Telemetry)
			defer tele.Shutdown()
			logger := log.New(os.Stdout, "[ORCH] ", log.LstdFlags)
			orch, err = core.NewOrchestrator(h.cfg, logger, tele)
			if err != nil {
				_ = h.store.FinishRun(ctx, runID, "failed", strPtr(err.Error()))
				return
			}
		}

		// construct thought from topic name/preferences
		thought := core.UserThought{
			ID:        runID,
			Content:   name,
			Timestamp: time.Now(),
		}
		// optional: pass preferences map into thought
		_ = prefsB

		result, err := orch.ProcessThought(ctx, thought)
		if err != nil {
			_ = h.store.FinishRun(ctx, runID, "failed", strPtr(err.Error()))
			return
		}

		// Persist the result using the same runID as key in app DB
		_ = h.store.UpsertProcessingResult(ctx, result)
		// Persist highlights and knowledge graph for topic name (as a simple key)
		if len(result.Highlights) > 0 {
			_ = h.store.SaveHighlights(ctx, name, result.Highlights)
		}
		_ = h.store.SaveKnowledgeGraphFromMetadata(ctx, name, result.Metadata)
		_ = h.store.FinishRun(ctx, runID, "succeeded", nil)
	}()

	return c.JSON(http.StatusAccepted, map[string]string{"run_id": runID})
}

func (h *RunsHandler) list(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	items, err := h.store.ListRuns(c.Request().Context(), topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, items)
}

func (h *RunsHandler) latest(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID, err := h.store.GetLatestRunID(c.Request().Context(), topicID)
	if err != nil || runID == "" {
		return echo.NewHTTPError(http.StatusNotFound, "no runs")
	}
	res, err := h.store.GetProcessingResultByID(c.Request().Context(), runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, res)
}

func strPtr(s string) *string { return &s }
