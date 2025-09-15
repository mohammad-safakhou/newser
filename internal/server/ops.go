package server

import (
    "net/http"

    "github.com/labstack/echo/v4"
    core "github.com/mohammad-safakhou/newser/internal/agent/core"
    "encoding/json"
    "html/template"
    "strings"
)

// OpsHandler exposes operational endpoints (metrics, performance summaries).
type OpsHandler struct {
    orch *core.Orchestrator
}

func NewOpsHandler(orch *core.Orchestrator) *OpsHandler { return &OpsHandler{orch: orch} }

// Register mounts ops endpoints under the provided group. It expects authentication to be applied by caller.
func (h *OpsHandler) Register(g *echo.Group) {
    g.GET("/performance", h.performance)
    g.GET("/dashboard", h.dashboard)
}

// performance returns orchestrator performance metrics and summaries.
//
//  @Summary  Performance metrics and summaries
//  @Tags     ops
//  @Security BearerAuth
//  @Security CookieAuth
//  @Produce  json
//  @Success  200 {object} map[string]interface{}
//  @Router   /api/ops/performance [get]
func (h *OpsHandler) performance(c echo.Context) error {
    data := h.orch.GetPerformanceMetrics()
    return c.JSON(http.StatusOK, data)
}

// dashboard returns a minimal HTML dashboard rendering key metrics without JS.
func (h *OpsHandler) dashboard(c echo.Context) error {
    m := h.orch.GetPerformanceMetrics()
    metrics, _ := m["metrics"].(interface{})
    summaries, _ := m["summaries"].(map[string]interface{})
    report, _ := m["report"].(string)
    var b strings.Builder
    b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Ops Dashboard</title></head><body style=\"font-family:system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif; color:#e5e7eb; background:#0f172a;\">")
    b.WriteString("<div style=\"max-width:960px;margin:24px auto;padding:0 16px\">")
    b.WriteString("<h1 style=\"font-size:18px;font-weight:600;margin-bottom:8px\">Operations Dashboard</h1>")
    b.WriteString("<pre style=\"background:#0b1220;border:1px solid #1f2937;border-radius:8px;padding:12px;overflow:auto\"><code>")
    if b2, err := json.MarshalIndent(metrics, "", "  "); err == nil { b.Write(b2) }
    b.WriteString("</code></pre>")
    if len(summaries) > 0 {
        b.WriteString("<h2 style=\"font-size:14px;font-weight:600;margin:16px 0 8px\">Summaries</h2>")
        b.WriteString("<pre style=\"background:#0b1220;border:1px solid #1f2937;border-radius:8px;padding:12px;overflow:auto\"><code>")
        if b3, err := json.MarshalIndent(summaries, "", "  "); err == nil { b.Write(b3) }
        b.WriteString("</code></pre>")
    }
    if report != "" {
        b.WriteString("<h2 style=\"font-size:14px;font-weight:600;margin:16px 0 8px\">Report</h2>")
        b.WriteString("<pre style=\"background:#0b1220;border:1px solid #1f2937;border-radius:8px;padding:12px;overflow:auto\">")
        b.WriteString(template.HTMLEscapeString(report))
        b.WriteString("</pre>")
    }
    b.WriteString("</div></body></html>")
    return c.HTML(http.StatusOK, b.String())
}
