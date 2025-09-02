package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/core"
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

// Trigger a new run for a topic
//
//	@Summary	Trigger run
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Produce	json
//	@Success	202	{object}	IDResponse	"Run accepted"
//	@Failure	404	{object}	HTTPError
//	@Failure	500	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/trigger [post]
func (h *RunsHandler) trigger(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	name, prefsB, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	var prefs Preferences
	err = json.Unmarshal(prefsB, &prefs)

	// create run
	runID, err := h.store.CreateRun(c.Request().Context(), topicID, "running")
	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}

	// launch background processing (use injected orchestrator)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		// construct thought from topic name/preferences
		thought := core.UserThought{
			ID:          runID,
			Content:     name,
			Preferences: prefs,
			Timestamp:   time.Now(),
		}
		// optional: pass preferences map into thought
		_ = prefsB

		result, err := h.orch.ProcessThought(ctx, thought)
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

	return c.JSON(http.StatusAccepted, IDResponse{ID: runID})
}

// List runs of a topic
//
//	@Summary	List runs
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Produce	json
//	@Success	200	{array}		store.Run
//	@Failure	404	{object}	HTTPError
//	@Failure	500	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs [get]
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

// Get the latest processing result for a topic
//
//	@Summary	Latest processing result
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Produce	json
//	@Success	200	{object}	map[string]interface{}
//	@Failure	404	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/latest_result [get]
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
