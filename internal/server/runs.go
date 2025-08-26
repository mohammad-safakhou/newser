package server

import (
    "context"
    "net/http"
    "time"

    "github.com/labstack/echo/v4"
    "github.com/mohammad-safakhou/newser/internal/agent/config"
    "github.com/mohammad-safakhou/newser/internal/agent/core"
    "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
    "github.com/mohammad-safakhou/newser/internal/store"
    "log"
    "os"
)

type RunsHandler struct {
    Store *store.Store
    Orch  *core.Orchestrator
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
    name, prefsB, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID)
    if err != nil { return echo.NewHTTPError(http.StatusNotFound, err.Error()) }

    // create run
    runID, err := h.Store.CreateRun(c.Request().Context(), topicID, "running")
    if err != nil { return c.NoContent(http.StatusInternalServerError) }

    // launch background processing (use injected orchestrator)
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
        defer cancel()

        orch := h.Orch
        if orch == nil {
            // Fallback: create once if not provided (should not happen)
            cfg, err := config.LoadConfig()
            if err != nil { _ = h.Store.FinishRun(ctx, runID, "failed", strPtr(err.Error())); return }
            tele := telemetry.NewTelemetry(cfg.Telemetry)
            defer tele.Shutdown()
            logger := log.New(os.Stdout, "[ORCH] ", log.LstdFlags)
            orch, err = core.NewOrchestrator(cfg, logger, tele)
            if err != nil { _ = h.Store.FinishRun(ctx, runID, "failed", strPtr(err.Error())); return }
        }

        // construct thought from topic name/preferences
        thought := core.UserThought{
            ID: runID,
            Content: name,
            Timestamp: time.Now(),
        }
        // optional: pass preferences map into thought
        _ = prefsB

        result, err := orch.ProcessThought(ctx, thought)
        if err != nil { _ = h.Store.FinishRun(ctx, runID, "failed", strPtr(err.Error())); return }

        // Persist the result using the same runID as key in app DB
        _ = h.Store.UpsertProcessingResult(ctx, result)
        // Persist highlights and knowledge graph for topic name (as a simple key)
        if len(result.Highlights) > 0 {
            _ = h.Store.SaveHighlights(ctx, name, result.Highlights)
        }
        _ = h.Store.SaveKnowledgeGraphFromMetadata(ctx, name, result.Metadata)
        _ = h.Store.FinishRun(ctx, runID, "succeeded", nil)
    }()

    return c.JSON(http.StatusAccepted, map[string]string{"run_id": runID})
}

func (h *RunsHandler) list(c echo.Context) error {
    userID := c.Get("user_id").(string)
    topicID := c.Param("topic_id")
    if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
        return echo.NewHTTPError(http.StatusNotFound, err.Error())
    }
    items, err := h.Store.ListRuns(c.Request().Context(), topicID)
    if err != nil { return echo.NewHTTPError(http.StatusInternalServerError, err.Error()) }
    return c.JSON(http.StatusOK, items)
}

func (h *RunsHandler) latest(c echo.Context) error {
    userID := c.Get("user_id").(string)
    topicID := c.Param("topic_id")
    if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
        return echo.NewHTTPError(http.StatusNotFound, err.Error())
    }
    runID, err := h.Store.GetLatestRunID(c.Request().Context(), topicID)
    if err != nil || runID == "" { return echo.NewHTTPError(http.StatusNotFound, "no runs") }
    res, err := h.Store.GetProcessingResultByID(c.Request().Context(), runID)
    if err != nil { return echo.NewHTTPError(http.StatusNotFound, err.Error()) }
    return c.JSON(http.StatusOK, res)
}

func strPtr(s string) *string { return &s }


