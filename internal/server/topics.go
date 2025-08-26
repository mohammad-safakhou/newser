package server

import (
    "encoding/json"
    "net/http"

    "github.com/labstack/echo/v4"
    "github.com/mohammad-safakhou/newser/internal/store"
    "github.com/mohammad-safakhou/newser/provider"
    "github.com/mohammad-safakhou/newser/models"
    "log"
)

type TopicsHandler struct {
    Store *store.Store
    LLM   provider.Provider
}

func (h *TopicsHandler) Register(g *echo.Group, secret []byte) {
    g.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, secret) })
    g.GET("", h.list)
    g.POST("", h.create)
    g.POST("/:id/chat", h.chat)
}

func (h *TopicsHandler) list(c echo.Context) error {
    userID := c.Get("user_id").(string)
    items, err := h.Store.ListTopics(c.Request().Context(), userID)
    if err != nil { return echo.NewHTTPError(http.StatusInternalServerError, err.Error()) }
    return c.JSON(http.StatusOK, items)
}

func (h *TopicsHandler) create(c echo.Context) error {
    userID := c.Get("user_id").(string)
    var req struct {
        Name string `json:"name"`
        Preferences map[string]interface{} `json:"preferences"`
        ScheduleCron string `json:"schedule_cron"`
    }
    if err := c.Bind(&req); err != nil { return echo.NewHTTPError(http.StatusBadRequest, err.Error()) }
    if req.ScheduleCron == "" { req.ScheduleCron = "@daily" }
    prefs, _ := json.Marshal(req.Preferences)
    id, err := h.Store.CreateTopic(c.Request().Context(), userID, req.Name, prefs, req.ScheduleCron)
    if err != nil { return echo.NewHTTPError(http.StatusInternalServerError, err.Error()) }
    return c.JSON(http.StatusCreated, map[string]string{"id": id})
}

// chat handles LLM-assisted conversation to refine a topic's goal/preferences
func (h *TopicsHandler) chat(c echo.Context) error {
    userID := c.Get("user_id").(string)
    topicID := c.Param("id")
    name, prefsB, cron, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID)
    if err != nil { return echo.NewHTTPError(http.StatusNotFound, err.Error()) }

    var prefs map[string]interface{}
    _ = json.Unmarshal(prefsB, &prefs)

    // Build models.Topic from DB row
    t := models.Topic{ Title: name, Preferences: prefs, CronSpec: cron, State: models.TopicStateInProgress }

    var req struct{ Message string `json:"message"` }
    if err := c.Bind(&req); err != nil || req.Message == "" { return echo.NewHTTPError(http.StatusBadRequest, "message required") }

    reply, updated, err := h.LLM.GeneralMessage(c.Request().Context(), req.Message, t)
    if err != nil { return echo.NewHTTPError(http.StatusInternalServerError, err.Error()) }

    // Persist updated preferences / cron
    newPrefs, _ := json.Marshal(updated.Preferences)
    var newCron *string
    if updated.CronSpec != "" { v := updated.CronSpec; newCron = &v }
    if err := h.Store.UpdateTopicPrefsAndCron(c.Request().Context(), topicID, userID, newPrefs, newCron); err != nil {
        log.Printf("update topic prefs error: %v", err)
        return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
    }

    return c.JSON(http.StatusOK, map[string]interface{}{
        "message": reply,
        "topic": updated,
    })
}


