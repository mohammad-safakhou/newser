package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/helpers"
	"github.com/mohammad-safakhou/newser/internal/policy"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type TopicsHandler struct {
	Store *store.Store
	LLM   agentcore.LLMProvider
	Model string
}

func (h *TopicsHandler) Register(g *echo.Group, secret []byte) {
	g.Use(runtime.EchoAuthMiddleware(secret))
	g.GET("", h.list)
	g.POST("", h.create)
	// Assist sub-group to avoid param route ambiguity
	assist := g.Group("/assist")
	assist.POST("/chat", h.assist)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update)
	g.PATCH("/:id/preferences", h.updatePreferences)
	g.PUT("/:id/policy", h.updatePolicy)
	g.PUT("/:id/budget", h.updateBudget)
	g.POST("/:id/chat", h.chat)
	g.GET("/:id/chat", h.chatHistory)
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
	pol := policy.NewDefault()
	if stored, ok, err := h.Store.GetUpdatePolicy(c.Request().Context(), topicID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("load policy: %v", err))
	} else if ok {
		pol = stored
	}
	budgetCfg, _, err := h.Store.GetTopicBudgetConfig(c.Request().Context(), topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("load budget: %v", err))
	}
	approval, hasPending, err := h.Store.GetPendingBudgetApproval(c.Request().Context(), topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("load budget approval: %v", err))
	}

	return c.JSON(http.StatusOK, TopicDetailResponse{
		ID:           topicID,
		Name:         name,
		ScheduleCron: cron,
		Preferences:  prefs,
		Policy:       temporalPolicyResponse(pol),
		Budget:       budgetResponse(budgetCfg),
		PendingBudget: func() *PendingApproval {
			if !hasPending {
				return nil
			}
			return pendingApprovalResponse(approval)
		}(),
	})
}

// update allows changing mutable topic fields (e.g., name). Name changes are user-driven only.
//
//	@Summary  Update topic
//	@Tags     topics
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    id      path  string true "Topic ID"
//	@Accept   json
//	@Produce  json
//	@Param    payload body  map[string]string true "Partial update (name)"
//	@Success  204
//	@Failure  400 {object} HTTPError
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{id} [patch]
func (h *TopicsHandler) update(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	// validate access
	if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&payload); err != nil || strings.TrimSpace(payload.Name) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name required")
	}
	if err := h.Store.UpdateTopicName(c.Request().Context(), topicID, userID, strings.TrimSpace(payload.Name)); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// updatePreferences allows updating the full preferences JSON and optionally cron
//
//	@Summary  Update topic preferences
//	@Tags     topics
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    id      path  string true "Topic ID"
//	@Accept   json
//	@Produce  json
//	@Param    payload body  map[string]interface{} true "Preferences update (preferences, optional schedule_cron)"
//	@Success  204
//	@Failure  400 {object} HTTPError
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{id}/preferences [patch]
func (h *TopicsHandler) updatePreferences(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var payload struct {
		Preferences  map[string]interface{} `json:"preferences"`
		ScheduleCron string                 `json:"schedule_cron"`
	}
	if err := c.Bind(&payload); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	b, err := json.Marshal(payload.Preferences)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	var cronPtr *string
	if strings.TrimSpace(payload.ScheduleCron) != "" {
		v := strings.TrimSpace(payload.ScheduleCron)
		cronPtr = &v
	}
	if err := h.Store.UpdateTopicPrefsAndCron(c.Request().Context(), topicID, userID, b, cronPtr); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// updatePolicy updates the temporal policy for a topic.
//
//	@Summary  Update topic temporal policy
//	@Tags     topics
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    id      path  string true "Topic ID"
//	@Accept   json
//	@Produce  json
//	@Param    payload body  UpdateTemporalPolicyRequest true "Temporal policy"
//	@Success  200 {object} TemporalPolicyResponse
//	@Failure  400 {object} HTTPError
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{id}/policy [put]
func (h *TopicsHandler) updatePolicy(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")

	if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	var req UpdateTemporalPolicyRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if strings.TrimSpace(req.RepeatMode) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "repeat_mode is required")
	}

	parseDuration := func(label string, value *string) (time.Duration, error) {
		if value == nil {
			return 0, nil
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			return 0, nil
		}
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, fmt.Errorf("%s must be a Go duration (e.g. 1h30m)", label)
		}
		if d < 0 {
			return 0, fmt.Errorf("%s cannot be negative", label)
		}
		return d, nil
	}

	refresh, err := parseDuration("refresh_interval", req.RefreshInterval)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	dedup, err := parseDuration("dedup_window", req.DedupWindow)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	fresh, err := parseDuration("freshness_threshold", req.FreshnessThreshold)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	pol := policy.UpdatePolicy{
		RefreshInterval:    refresh,
		DedupWindow:        dedup,
		RepeatMode:         policy.RepeatMode(strings.TrimSpace(req.RepeatMode)),
		FreshnessThreshold: fresh,
		Metadata:           req.Metadata,
	}
	if err := pol.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := h.Store.UpsertUpdatePolicy(c.Request().Context(), topicID, pol); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, temporalPolicyResponse(pol))
}

func temporalPolicyResponse(pol policy.UpdatePolicy) *TemporalPolicyResponse {
	resp := &TemporalPolicyResponse{
		RepeatMode: string(pol.RepeatMode),
	}
	if pol.RefreshInterval > 0 {
		resp.RefreshInterval = pol.RefreshInterval.String()
	}
	if pol.DedupWindow > 0 {
		resp.DedupWindow = pol.DedupWindow.String()
	}
	if pol.FreshnessThreshold > 0 {
		resp.FreshnessThreshold = pol.FreshnessThreshold.String()
	}
	if len(pol.Metadata) > 0 {
		resp.Metadata = pol.Metadata
	}
	return resp
}

// updateBudget updates the budget guardrails for a topic.
//
//	@Summary  Update topic budget
//	@Tags     topics
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    id      path  string true "Topic ID"
//	@Accept   json
//	@Produce  json
//	@Param    payload body  UpdateBudgetConfigRequest true "Budget config"
//	@Success  200 {object} BudgetConfigResponse
//	@Failure  400 {object} HTTPError
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{id}/budget [put]
func (h *TopicsHandler) updateBudget(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var req UpdateBudgetConfigRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	current, _, err := h.Store.GetTopicBudgetConfig(c.Request().Context(), topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("load budget: %v", err))
	}
	cfg := current.Clone()
	if req.MaxCost != nil {
		v := *req.MaxCost
		cfg.MaxCost = &v
	}
	if req.MaxTokens != nil {
		v := *req.MaxTokens
		cfg.MaxTokens = &v
	}
	if req.MaxTimeSeconds != nil {
		v := *req.MaxTimeSeconds
		cfg.MaxTimeSeconds = &v
	}
	if req.ApprovalThreshold != nil {
		v := *req.ApprovalThreshold
		cfg.ApprovalThreshold = &v
	}
	if req.RequireApproval != nil {
		cfg.RequireApproval = *req.RequireApproval
	}
	if req.Metadata != nil {
		cfg.Metadata = req.Metadata
	}
	if err := cfg.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := h.Store.UpsertTopicBudgetConfig(c.Request().Context(), topicID, cfg); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, budgetResponse(cfg))
}

func budgetResponse(cfg budget.Config) *BudgetConfigResponse {
	if cfg.MaxCost == nil && cfg.MaxTokens == nil && cfg.MaxTimeSeconds == nil && cfg.ApprovalThreshold == nil && !cfg.RequireApproval && len(cfg.Metadata) == 0 {
		return nil
	}
	resp := &BudgetConfigResponse{RequireApproval: cfg.RequireApproval}
	if cfg.MaxCost != nil {
		v := *cfg.MaxCost
		resp.MaxCost = &v
	}
	if cfg.MaxTokens != nil {
		v := *cfg.MaxTokens
		resp.MaxTokens = &v
	}
	if cfg.MaxTimeSeconds != nil {
		v := *cfg.MaxTimeSeconds
		resp.MaxTimeSeconds = &v
	}
	if cfg.ApprovalThreshold != nil {
		v := *cfg.ApprovalThreshold
		resp.ApprovalThreshold = &v
	}
	if len(cfg.Metadata) > 0 {
		clone := make(map[string]interface{}, len(cfg.Metadata))
		for k, v := range cfg.Metadata {
			clone[k] = v
		}
		resp.Metadata = clone
	}
	return resp
}

func pendingApprovalResponse(rec store.BudgetApprovalRecord) *PendingApproval {
	return &PendingApproval{
		RunID:         rec.RunID,
		EstimatedCost: rec.EstimatedCost,
		Threshold:     rec.Threshold,
		CreatedAt:     rec.CreatedAt.Format(time.RFC3339),
		RequestedBy:   rec.RequestedBy,
	}
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
	// persist the user message before LLM call
	if _, err := h.Store.CreateChatMessage(c.Request().Context(), topicID, userID, "user", req.Message); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
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
	// persist assistant reply
	_, _ = h.Store.CreateChatMessage(c.Request().Context(), topicID, userID, "assistant", reply)
	return c.JSON(http.StatusOK, ChatResponse{
		Message: reply,
		Topic: map[string]interface{}{
			"Title": name, "Preferences": newPrefs, "CronSpec": newCron},
	})
}

// chatHistory returns paginated chat messages for a topic (newest first, limited)
//
//	@Summary  List chat messages for topic
//	@Tags     topics
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    id       path   string true  "Topic ID"
//	@Param    limit    query  int    false "Max messages (default 20, max 200)"
//	@Param    before   query  string false "ISO timestamp; return messages created before this time"
//	@Produce  json
//	@Success  200 {array} ChatLogMessage
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{id}/chat [get]
func (h *TopicsHandler) chatHistory(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("id")
	// validate topic ownership
	if _, _, _, err := h.Store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	limit := 20
	if s := c.QueryParam("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	var before *time.Time
	if b := c.QueryParam("before"); b != "" {
		if ts, err := time.Parse(time.RFC3339, b); err == nil {
			before = &ts
		}
	}
	msgs, err := h.Store.ListChatMessages(c.Request().Context(), topicID, userID, limit, before)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// map to API model
	out := make([]ChatLogMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ChatLogMessage{ID: m.ID, Role: m.Role, Content: m.Content, CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return c.JSON(http.StatusOK, out)
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
	// Include agent capability hints so the assistant knows which parameters to collect/refine.
	caps := agentcore.CapabilitiesDoc(nil)
	// Do not use or modify the user-defined topic name in guidance. Keep name out of semantics.
	prompt := fmt.Sprintf(`You are assisting configuration of a news topic.
Current schedule: %s
Current preferences (JSON): %s
User request: %s

Agent parameter hints (use to refine preferences/search fields):
%s

Instructions:
- Infer sensible defaults (e.g., search_lang/ui_lang from user language if clear), but ALWAYS allow the user to override later.
- When adding or updating preferences, keep them under explicit keys (e.g., preferences.search.country, preferences.search.search_lang, etc.).
- If important parameters are missing or ambiguous, include a concise follow-up question in "message" to clarify.
- Do NOT modify the user-defined topic name.

Respond ONLY as strict JSON with keys:
{"message": string, "preferences": object, "cron_spec": string, "context_summary": string, "objectives": [string]}
`, cron, mustJSON(prefs), message, caps)
	out, err := llm.Generate(ctx, prompt, model, nil)
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
	out, err = helpers.ExtractJSON(out)
	if err != nil {
		return "", nil, "", fmt.Errorf("extract JSON: %w", err)
	}
	if e := json.Unmarshal([]byte(out), &parsed); e != nil {
		var tmp map[string]interface{}
		if i := firstJSONIndex(out); i >= 0 {
			_ = json.Unmarshal([]byte(out[i:]), &tmp)
		}
		if tmp == nil {
			return "", nil, "", fmt.Errorf("invalid LLM response: %s", out)
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
