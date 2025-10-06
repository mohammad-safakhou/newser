package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type builderStore interface {
	GetTopicByID(ctx context.Context, topicID, userID string) (string, []byte, string, error)
	SaveBuilderSchema(ctx context.Context, topicID, kind, authorID string, content json.RawMessage) (store.BuilderSchemaRecord, error)
	GetLatestBuilderSchema(ctx context.Context, topicID, kind string) (store.BuilderSchemaRecord, bool, error)
	GetBuilderSchemaByVersion(ctx context.Context, topicID, kind string, version int) (store.BuilderSchemaRecord, bool, error)
	ListBuilderSchemaHistory(ctx context.Context, topicID, kind string, limit int) ([]store.BuilderSchemaRecord, error)
}

type BuilderHandler struct {
	store    builderStore
	defaults map[string]json.RawMessage
}

func NewBuilderHandler(cfg *config.Config, st builderStore) *BuilderHandler {
	if st == nil {
		return nil
	}
	defaults := map[string]json.RawMessage{}
	clone := func(raw string) json.RawMessage {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		return json.RawMessage([]byte(raw))
	}
	var builderCfg config.BuilderConfig
	if cfg != nil {
		builderCfg = cfg.Builder
	}
	if def := clone(builderCfg.Defaults.Topic); def != nil {
		defaults["topic"] = def
	} else {
		defaults["topic"] = json.RawMessage([]byte(config.DefaultTopicSchema))
	}
	if def := clone(builderCfg.Defaults.Blueprint); def != nil {
		defaults["blueprint"] = def
	} else {
		defaults["blueprint"] = json.RawMessage([]byte(config.DefaultBlueprintSchema))
	}
	if def := clone(builderCfg.Defaults.View); def != nil {
		defaults["view"] = def
	} else {
		defaults["view"] = json.RawMessage([]byte(config.DefaultViewSchema))
	}
	if def := clone(builderCfg.Defaults.Route); def != nil {
		defaults["route"] = def
	} else {
		defaults["route"] = json.RawMessage([]byte(config.DefaultRouteSchema))
	}
	return &BuilderHandler{store: st, defaults: defaults}
}

func (h *BuilderHandler) Register(g *echo.Group, secret []byte) {
	if h == nil {
		return
	}
	grp := g.Group("/:topic_id/builder")
	grp.Use(runtime.EchoAuthMiddleware(secret))
	grp.GET("/schema", h.getLatest)
	grp.POST("/schema", h.saveSchema)
	grp.GET("/history", h.history)
	grp.POST("/rollback", h.rollback)
	grp.GET("/diff", h.diff)
}

type builderSchemaRequest struct {
	Kind     string          `json:"kind"`
	Content  json.RawMessage `json:"content"`
	AuthorID string          `json:"author_id,omitempty"`
}

type builderHistoryItem struct {
	Version   int             `json:"version"`
	Content   json.RawMessage `json:"content"`
	AuthorID  *string         `json:"author_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type builderDiffResponse struct {
	Kind        string          `json:"kind"`
	FromVersion int             `json:"from_version"`
	From        json.RawMessage `json:"from"`
	ToVersion   int             `json:"to_version"`
	To          json.RawMessage `json:"to"`
}

func (h *BuilderHandler) getLatest(c echo.Context) error {
	ctx := c.Request().Context()
	topicID, userID, kind, err := h.parseTopicKind(c)
	if err != nil {
		return err
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	rec, ok, err := h.store.GetLatestBuilderSchema(ctx, topicID, kind)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		if def, ok := h.defaults[kind]; ok {
			return c.JSON(http.StatusOK, builderHistoryItem{Version: 0, Content: cloneRaw(def)})
		}
		return echo.NewHTTPError(http.StatusNotFound, "schema not found")
	}
	return c.JSON(http.StatusOK, historyItemFromRecord(rec))
}

func (h *BuilderHandler) saveSchema(c echo.Context) error {
	ctx := c.Request().Context()
	topicID := c.Param("topic_id")
	userID, _ := c.Get("user_id").(string)
	if strings.TrimSpace(topicID) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "topic_id required")
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var req builderSchemaRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	kind := normalizeBuilderKind(req.Kind)
	if kind == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "kind must be one of topic, blueprint, view, route")
	}
	if len(req.Content) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "content required")
	}
	rec, err := h.store.SaveBuilderSchema(ctx, topicID, kind, req.AuthorID, req.Content)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, historyItemFromRecord(rec))
}

func (h *BuilderHandler) history(c echo.Context) error {
	ctx := c.Request().Context()
	topicID, userID, kind, err := h.parseTopicKind(c)
	if err != nil {
		return err
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	limit := 10
	if val := strings.TrimSpace(c.QueryParam("limit")); val != "" {
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			limit = v
		}
	}
	records, err := h.store.ListBuilderSchemaHistory(ctx, topicID, kind, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if len(records) == 0 {
		if def, ok := h.defaults[kind]; ok {
			return c.JSON(http.StatusOK, []builderHistoryItem{{Version: 0, Content: cloneRaw(def)}})
		}
		return c.JSON(http.StatusOK, []builderHistoryItem{})
	}
	out := make([]builderHistoryItem, 0, len(records))
	for _, rec := range records {
		out = append(out, historyItemFromRecord(rec))
	}
	return c.JSON(http.StatusOK, out)
}

func (h *BuilderHandler) rollback(c echo.Context) error {
	ctx := c.Request().Context()
	topicID, userID, kind, err := h.parseTopicKind(c)
	if err != nil {
		return err
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	var req struct {
		Version  int    `json:"version"`
		AuthorID string `json:"author_id,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Version <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "version must be positive")
	}
	rec, ok, err := h.store.GetBuilderSchemaByVersion(ctx, topicID, kind, req.Version)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "version not found")
	}
	saved, err := h.store.SaveBuilderSchema(ctx, topicID, kind, req.AuthorID, rec.Content)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, historyItemFromRecord(saved))
}

func (h *BuilderHandler) diff(c echo.Context) error {
	ctx := c.Request().Context()
	topicID, userID, kind, err := h.parseTopicKind(c)
	if err != nil {
		return err
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	fromVersion, toVersion, err := parseDiffVersions(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	fromRec, ok, err := h.store.GetBuilderSchemaByVersion(ctx, topicID, kind, fromVersion)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "from version not found")
	}
	toRec, ok, err := h.store.GetBuilderSchemaByVersion(ctx, topicID, kind, toVersion)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "to version not found")
	}
	resp := builderDiffResponse{
		Kind:        kind,
		FromVersion: fromRec.Version,
		From:        fromRec.Content,
		ToVersion:   toRec.Version,
		To:          toRec.Content,
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *BuilderHandler) parseTopicKind(c echo.Context) (topicID, userID, kind string, err error) {
	topicID = c.Param("topic_id")
	userID, _ = c.Get("user_id").(string)
	kind = normalizeBuilderKind(c.QueryParam("kind"))
	if kind == "" {
		err = echo.NewHTTPError(http.StatusBadRequest, "kind must be one of topic, blueprint, view, route")
	}
	return
}

func normalizeBuilderKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		k = "topic"
	}
	switch k {
	case "topic", "blueprint", "view", "route":
		return k
	default:
		return ""
	}
}

func historyItemFromRecord(rec store.BuilderSchemaRecord) builderHistoryItem {
	return builderHistoryItem{
		Version:   rec.Version,
		Content:   cloneRaw(rec.Content),
		AuthorID:  rec.AuthorID,
		CreatedAt: rec.CreatedAt,
	}
}

func parseDiffVersions(c echo.Context) (int, int, error) {
	fromStr := strings.TrimSpace(c.QueryParam("from"))
	toStr := strings.TrimSpace(c.QueryParam("to"))
	if fromStr == "" || toStr == "" {
		return 0, 0, fmt.Errorf("from and to parameters required")
	}
	fromVersion, err := strconv.Atoi(fromStr)
	if err != nil || fromVersion <= 0 {
		return 0, 0, fmt.Errorf("invalid from version")
	}
	toVersion, err := strconv.Atoi(toStr)
	if err != nil || toVersion <= 0 {
		return 0, 0, fmt.Errorf("invalid to version")
	}
	return fromVersion, toVersion, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return json.RawMessage(cp)
}
