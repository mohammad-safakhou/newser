package server

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strings"

	"github.com/labstack/echo/v4"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type TopicsHandler struct {
    Store *store.Store
    LLM   agentcore.LLMProvider
    Model string
}

func (h *TopicsHandler) Register(g *echo.Group, secret []byte) {
    g.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return withAuth(next, secret) })
    g.GET("", h.list)
    g.POST("", h.create)
    // Assist sub-group to avoid param route ambiguity
    assist := g.Group("/assist")
    assist.POST("/chat", h.assist)
    g.GET("/:id", h.get)
    g.PATCH("/:id", h.update)
    g.POST("/:id/chat", h.chat)
}

// List topics
//
//	@Summary	List topics
//	@Tags		topics
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Produce	json
//	@Success	200	{array}		store.Topic
//	@Failure	500	{object}	HTTPError
//	@Router		/api/topics [get]
func (h *TopicsHandler) list(c echo.Context) error {
	userID := c.Get("user_id").(string)
	items, err := h.Store.ListTopics(c.Request().Context(), userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, items)
}

// Create topic
//
//	@Summary	Create topic
//	@Tags		topics
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		CreateTopicRequest	true	"New topic"
//	@Success	201		{object}	IDResponse
//	@Failure	400		{object}	HTTPError
//	@Failure	500		{object}	HTTPError
//	@Router		/api/topics [post]
func (h *TopicsHandler) create(c echo.Context) error {
	userID := c.Get("user_id").(string)
	var req CreateTopicRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.ScheduleCron == "" {
		req.ScheduleCron = "@daily"
	}
	prefs, _ := json.Marshal(req.Preferences)
	id, err := h.Store.CreateTopic(c.Request().Context(), userID, req.Name, prefs, req.ScheduleCron)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, IDResponse{ID: id})
}

// get returns detailed topic with preferences
//
//	@Summary	Get topic
//	@Tags		topics
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		id	path	string	true	"Topic ID"
//	@Produce	json
//	@Success	200	{object}	TopicDetailResponse
//	@Failure	404	{object}	HTTPError
//	@Router		/api/topics/{id} [get]
func (h *TopicsHandler) get(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	name, prefsB, cron, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var prefs map[string]interface{}
	_ = json.Unmarshal(prefsB, &prefs)
	return c.JSON(http.StatusOK, TopicDetailResponse{
		ID:           topicID,
		Name:         name,
		ScheduleCron: cron,
		Preferences:  prefs,
	})
}

// update allows changing mutable topic fields (e.g., name). Name changes are user-driven only.
//
//  @Summary  Update topic
//  @Tags     topics
//  @Security BearerAuth
//  @Security CookieAuth
//  @Param    id      path  string true "Topic ID"
//  @Accept   json
//  @Produce  json
//  @Param    payload body  map[string]string true "Partial update (name)"
//  @Success  204
//  @Failure  400 {object} HTTPError
//  @Failure  404 {object} HTTPError
//  @Router   /api/topics/{id} [patch]
func (h *TopicsHandler) update(c echo.Context) error {
    userID := c.Get("user_id").(string)
    topicID := c.Param("id")
    // validate access
    if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
        return echo.NewHTTPError(http.StatusNotFound, err.Error())
    }
    var payload struct{ Name string `json:"name"` }
    if err := c.Bind(&payload); err != nil || strings.TrimSpace(payload.Name) == "" {
        return echo.NewHTTPError(http.StatusBadRequest, "name required")
    }
    if err := h.Store.UpdateTopicName(c.Request().Context(), topicID, userID, strings.TrimSpace(payload.Name)); err != nil {
        return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
    }
    return c.NoContent(http.StatusNoContent)
}

// chat handles LLM-assisted conversation to refine a topic's goal/preferences
//
//	@Summary	Chat with topic assistant
//	@Tags		topics
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		id	path	string	true	"Topic ID"
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		ChatRequest	true	"Chat message"
//	@Success	200		{object}	ChatResponse
//	@Failure	400		{object}	HTTPError
//	@Failure	404		{object}	HTTPError
//	@Failure	503		{object}	HTTPError
//	@Router		/api/topics/{id}/chat [post]
func (h *TopicsHandler) chat(c echo.Context) error {
	if h.LLM == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "LLM not configured")
	}
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	name, prefsB, cron, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var prefs map[string]interface{}
	_ = json.Unmarshal(prefsB, &prefs)
	var req ChatRequest
	if err := c.Bind(&req); err != nil || req.Message == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "message required")
	}
	reply, newPrefs, newCron, err := llmRefineTopic(c.Request().Context(), h.LLM, h.Model, req.Message, name, prefs, cron)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	prefsJSON, _ := json.Marshal(newPrefs)
	var newCronPtr *string
	if newCron != "" {
		v := newCron
		newCronPtr = &v
	}
	if err := h.Store.UpdateTopicPrefsAndCron(c.Request().Context(), topicID, userID, prefsJSON, newCronPtr); err != nil {
		log.Printf("update topic prefs error: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, ChatResponse{
		Message: reply,
		Topic: map[string]interface{}{
			"Title": name, "Preferences": newPrefs, "CronSpec": newCron},
	})
}

// assist provides LLM guidance for drafting a new topic before persistence
//
//	@Summary	Draft a new topic with LLM
//	@Tags		topics
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		AssistRequest	true	"Assist request"
//	@Success	200		{object}	AssistResponse
//	@Failure	400		{object}	HTTPError
//	@Failure	503		{object}	HTTPError
//	@Router		/api/topics/assist/chat [post]
func (h *TopicsHandler) assist(c echo.Context) error {
	if h.LLM == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "LLM not configured")
	}
	var req AssistRequest
	if err := c.Bind(&req); err != nil || req.Message == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "message required")
	}
	reply, newPrefs, newCron, err := llmRefineTopic(c.Request().Context(), h.LLM, h.Model, req.Message, req.Name, req.Preferences, req.ScheduleCron)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, AssistResponse{Message: reply, Topic: map[string]interface{}{"Title": req.Name, "Preferences": newPrefs, "CronSpec": newCron}})
}

// llmRefineTopic crafts a prompt and parses a strict JSON response to update preferences/cron
func llmRefineTopic(ctx context.Context, llm agentcore.LLMProvider, model string, message string, name string, prefs map[string]interface{}, cron string) (reply string, newPrefs map[string]interface{}, newCron string, err error) {
    // Do not use or modify the user-defined topic name in guidance. Keep name out of semantics.
    prompt := fmt.Sprintf(`You are assisting configuration of a news topic.
Current schedule: %s
Current preferences (JSON): %s
User request: %s

Respond ONLY as strict JSON with keys:
{"message": string, "preferences": object, "cron_spec": string, "context_summary": string, "objectives": [string]}
`, cron, mustJSON(prefs), message)
    out, err := llm.Generate(ctx, prompt, model, map[string]interface{}{})
	if err != nil {
		return "", nil, "", err
	}
	var parsed struct {
		Message        string                 `json:"message"`
		Preferences    map[string]interface{} `json:"preferences"`
		CronSpec       string                 `json:"cron_spec"`
		ContextSummary string                 `json:"context_summary"`
		Objectives     []string               `json:"objectives"`
	}
	if e := json.Unmarshal([]byte(out), &parsed); e != nil {
		var tmp map[string]interface{}
		if i := firstJSONIndex(out); i >= 0 {
			_ = json.Unmarshal([]byte(out[i:]), &tmp)
		}
		if tmp == nil {
			return out, prefs, cron, nil
		}
		b, _ := json.Marshal(tmp)
		_ = json.Unmarshal(b, &parsed)
	}
	if parsed.Preferences == nil {
		parsed.Preferences = prefs
	}
	if parsed.CronSpec == "" {
		parsed.CronSpec = cron
	}
	if parsed.ContextSummary != "" {
		if parsed.Preferences == nil {
			parsed.Preferences = map[string]interface{}{}
		}
		parsed.Preferences["context_summary"] = parsed.ContextSummary
	}
	if len(parsed.Objectives) > 0 {
		if parsed.Preferences == nil {
			parsed.Preferences = map[string]interface{}{}
		}
		parsed.Preferences["objectives"] = parsed.Objectives
	}
	return parsed.Message, parsed.Preferences, parsed.CronSpec, nil
}

func mustJSON(v interface{}) string { b, _ := json.Marshal(v); return string(b) }
func firstJSONIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			return i
		}
	}
	return -1
}
