package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// ToolsHandler exposes capability registry endpoints.
type ToolsHandler struct {
	Store  *store.Store
	Config *config.Config
}

func (h *ToolsHandler) Register(g *echo.Group, secret []byte) {
	g.Use(runtime.EchoAuthMiddleware(secret))
	g.GET("", h.list)
	g.POST("", h.publish)
}

func (h *ToolsHandler) list(c echo.Context) error {
	records, err := h.Store.ListToolCards(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	out, err := runtime.ToolCardsFromRecords(records)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, out)
}

type publishRequest struct {
	ToolCard capability.ToolCard `json:"tool_card"`
}

func (h *ToolsHandler) publish(c echo.Context) error {
	var req publishRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	tc := req.ToolCard
	if tc.Name == "" || tc.Version == "" || tc.AgentType == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name, version, and agent_type are required")
	}
	checksum, err := capability.ComputeChecksum(tc)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if tc.Checksum != "" && tc.Checksum != checksum {
		return echo.NewHTTPError(http.StatusBadRequest, "checksum mismatch")
	}
	tc.Checksum = checksum
	sig, err := capability.SignToolCard(tc, h.Config.Capability.SigningSecret)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	tc.Signature = sig
	rec, err := runtime.ToolCardRecordFromToolCard(tc)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := h.Store.UpsertToolCard(c.Request().Context(), rec); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, tc)
}
