package server

import (
    "encoding/json"
    "net/http"

    "github.com/labstack/echo/v4"
    "github.com/mohammad-safakhou/newser/internal/store"
)

type TopicsHandler struct {
    Store *store.Store
}

func (h *TopicsHandler) Register(g *echo.Group, secret []byte) {
    g.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, secret) })
    g.GET("", h.list)
    g.POST("", h.create)
}

func (h *TopicsHandler) list(c echo.Context) error {
    userID := c.Get("user_id").(string)
    items, err := h.Store.ListTopics(c.Request().Context(), userID)
    if err != nil { return c.NoContent(http.StatusInternalServerError) }
    return c.JSON(http.StatusOK, items)
}

func (h *TopicsHandler) create(c echo.Context) error {
    userID := c.Get("user_id").(string)
    var req struct {
        Name string `json:"name"`
        Preferences map[string]interface{} `json:"preferences"`
        ScheduleCron string `json:"schedule_cron"`
    }
    if err := c.Bind(&req); err != nil { return c.NoContent(http.StatusBadRequest) }
    if req.ScheduleCron == "" { req.ScheduleCron = "@daily" }
    prefs, _ := json.Marshal(req.Preferences)
    id, err := h.Store.CreateTopic(c.Request().Context(), userID, req.Name, prefs, req.ScheduleCron)
    if err != nil { return c.NoContent(http.StatusInternalServerError) }
    return c.JSON(http.StatusCreated, map[string]string{"id": id})
}


