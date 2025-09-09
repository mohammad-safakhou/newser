package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
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
	g.GET("/:topic_id/runs/:run_id/result", h.result)
	g.POST("/:topic_id/runs/:run_id/expand", h.expand)
	g.GET("/:topic_id/runs/:run_id/markdown", h.markdown)
	g.POST("/:topic_id/runs/:run_id/expand_all", h.expandAll)
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
    _, prefsB, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
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

    // launch background processing using shared pipeline
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
        defer cancel()
        if err := processRun(ctx, h.cfg, h.store, h.orch, topicID, userID, runID); err != nil {
            _ = h.store.FinishRun(ctx, runID, "failed", strPtr(err.Error()))
        }
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

// Get a specific run's processing result by run_id
//
//	@Summary	Run result by ID
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		run_id		path	string	true	"Run ID"
//	@Produce	json
//	@Success	200	{object}	map[string]interface{}
//	@Failure	404	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/{run_id}/result [get]
func (h *RunsHandler) result(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	res, err := h.store.GetProcessingResultByID(c.Request().Context(), runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, res)
}

// Expand a highlight/source from a run into a deeper Markdown explanation
//
//	@Summary	Expand a run item
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		run_id		path	string	true	"Run ID"
//	@Accept		json
//	@Produce	json
//	@Param		payload		body		ExpandRequest	true	"Expand request"
//	@Success	200		{object}	ExpandResponse
//	@Failure	404		{object}	HTTPError
//	@Failure	500		{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/{run_id}/expand [post]
func (h *RunsHandler) expand(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	var req ExpandRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	res, err := h.store.GetProcessingResultByID(c.Request().Context(), runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	summary, _ := res["summary"].(string)
	detailed, _ := res["detailed_report"].(string)
	var target string
	if req.HighlightIndex != nil {
		if hs, ok := res["highlights"].([]interface{}); ok {
			idx := *req.HighlightIndex
			if idx >= 0 && idx < len(hs) {
				if hmap, ok := hs[idx].(map[string]interface{}); ok {
					if v, ok := hmap["content"].(string); ok {
						target = v
					} else if v2, ok := hmap["title"].(string); ok {
						target = v2
					}
				}
			}
		}
	}
	if target == "" && req.SourceURL != "" {
		target = "Focus URL: " + req.SourceURL
	}
	if target == "" {
		target = "(no specific highlight chosen)"
	}

	llm, err := core.NewLLMProvider(h.cfg.LLM)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	model := h.cfg.LLM.Routing.Synthesis
	if model == "" {
		model = h.cfg.LLM.Routing.Chatting
	}

	prompt := fmt.Sprintf(`You are generating a deeper, actionable markdown brief for a news item.
USER SUMMARY:
%s

DETAILED REPORT SNIPPET:
%s

TARGET:
%s

FOCUS (optional): %s

Guidance:
- Provide concrete details: what happened, why it matters, timeline, how-to actions if relevant.
- Use clear section headings.
- Include links when available (from context, otherwise omit).
- Keep it factual and helpful; avoid speculation.

Return ONLY the markdown content.`, summary, firstN(detailed, 1500), target, req.Focus)

	out, err := llm.Generate(c.Request().Context(), prompt, model, map[string]interface{}{"temperature": 0.3, "max_tokens": 1200})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, ExpandResponse{Markdown: out})
}

func strPtr(s string) *string { return &s }

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ExpandAll: Deprecated. Returns stored final report.
//
//	@Summary  Expand an entire run to deep-dive markdown
//	@Tags     runs
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    topic_id path string true "Topic ID"
//	@Param    run_id   path string true "Run ID"
//	@Accept   json
//	@Produce  json
//	@Param    payload body ExpandAllRequest true "Expand all request"
//	@Success  200 {object} ExpandAllResponse
//	@Failure  404 {object} HTTPError
//	@Failure  500 {object} HTTPError
//	@Router   /api/topics/{topic_id}/runs/{run_id}/expand_all [post]
func (h *RunsHandler) expandAll(c echo.Context) error {
	// Deprecated: return stored final report (no on-demand generation)
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	if b, err := os.ReadFile(runMarkdownPath(topicID, runID)); err == nil && len(b) > 0 {
		return c.JSON(http.StatusOK, ExpandAllResponse{Markdown: string(b)})
	}
	// fallback to renderMarkdownReport
	name, _, _, _ := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
	res, err := h.store.GetProcessingResultByID(c.Request().Context(), runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	md := renderMarkdownReport(name, res)
	if md == "" {
		return echo.NewHTTPError(http.StatusNotFound, "no report available")
	}
	return c.JSON(http.StatusOK, ExpandAllResponse{Markdown: md})
}

// processRun executes the full run pipeline shared by HTTP trigger and scheduler
func processRun(ctx context.Context, cfg *config.Config, st *store.Store, orch *core.Orchestrator, topicID string, userID string, runID string) error {
    // Build context from previous runs and knowledge graph
    ctxMap := map[string]interface{}{}
    if ts, _ := st.LatestRunTime(ctx, topicID); ts != nil {
        ctxMap["last_run_time"] = ts.UTC().Format(time.RFC3339)
    }
    if rid, _ := st.GetLatestRunID(ctx, topicID); rid != "" {
        if prev, err := st.GetProcessingResultByID(ctx, rid); err == nil {
            ctxMap["prev_summary"] = prev["summary"]
            var known []string
            if sl, ok := prev["sources"].([]interface{}); ok {
                for _, it := range sl {
                    if m, ok := it.(map[string]interface{}); ok {
                        if u, ok := m["url"].(string); ok && u != "" { known = append(known, u) }
                    }
                }
            }
            if len(known) > 0 { ctxMap["known_urls"] = known }
        }
    }
    if kg, err := st.GetKnowledgeGraph(ctx, topicID); err == nil {
        ctxMap["knowledge_graph"] = map[string]interface{}{"nodes": kg.Nodes, "edges": kg.Edges, "last_updated": kg.LastUpdated}
    }

    // Fetch topic (name, preferences)
    name, prefsB, _, err := st.GetTopicByID(ctx, topicID, userID)
    if err != nil { return err }
    var prefs Preferences
    _ = json.Unmarshal(prefsB, &prefs)

    // Construct thought from preferences (not user-defined name)
    content := deriveThoughtContentFromPrefs(map[string]interface{}(prefs))
    thought := core.UserThought{ ID: runID, Content: content, Preferences: prefs, Timestamp: time.Now(), Context: ctxMap }

    // Ensure orchestrator exists
    var o *core.Orchestrator = orch
    if o == nil {
        tele := telemetry.NewTelemetry(cfg.Telemetry)
        defer tele.Shutdown()
        logger := log.New(log.Writer(), "[RUN] ", log.LstdFlags)
        var err2 error
        o, err2 = core.NewOrchestrator(cfg, logger, tele)
        if err2 != nil { return err2 }
    }

    result, err := o.ProcessThought(ctx, thought)
    if err != nil { return err }

    // Persist
    _ = st.UpsertProcessingResult(ctx, result)
    if len(result.Highlights) > 0 { _ = st.SaveHighlights(ctx, topicID, result.Highlights) }
    _ = st.SaveKnowledgeGraphFromMetadata(ctx, topicID, result.Metadata)
    if resJSON, err := st.GetProcessingResultByID(ctx, runID); err == nil {
        if md, derr := generateFullReport(ctx, cfg, name, prefs, resJSON); derr == nil && md != "" {
            _ = writeRunMarkdown(topicID, runID, md)
            _ = st.FinishRun(ctx, runID, "succeeded", nil)
        } else {
            _ = st.FinishRun(ctx, runID, "failed", strPtr(fmt.Sprintf("full report generation failed: %v", derr)))
        }
    } else {
        _ = st.FinishRun(ctx, runID, "failed", strPtr("processing result missing"))
    }
    return nil
}

// generateFullReport creates the final deep-dive Markdown for a run using the configured LLM provider.
func generateFullReport(ctx context.Context, cfg *config.Config, topicName string, prefs Preferences, res map[string]interface{}) (string, error) {
    llm, err := core.NewLLMProvider(cfg.LLM)
    if err != nil {
        return "", err
    }
    model := cfg.LLM.Routing.Synthesis
    if model == "" { model = cfg.LLM.Routing.Chatting }

	highlights := "[]"
	if hs, ok := res["highlights"].([]interface{}); ok {
		b, _ := json.Marshal(hs)
		highlights = string(b)
	}
	sources := "[]"
	if ss, ok := res["sources"].([]interface{}); ok {
		trimmed := make([]map[string]interface{}, 0, len(ss))
		for _, it := range ss {
			if m, ok := it.(map[string]interface{}); ok {
				trimmed = append(trimmed, map[string]interface{}{"title": m["title"], "url": m["url"], "type": m["type"]})
			}
		}
		b, _ := json.Marshal(trimmed)
		sources = string(b)
	}
	summary := safeString(res["summary"])
	detailed := firstN(safeString(res["detailed_report"]), 2000)

	prompt := fmt.Sprintf(`Create a single, comprehensive Markdown report for the following run. No grouping options; produce a cohesive narrative suitable for end users.
TOPIC: %s

SUMMARY:\n%s

DETAILED REPORT (snippet):\n%s

HIGHLIGHTS (JSON): %s
SOURCES (JSON): %s

Guidance:
- Start with a clear title and short executive summary.
- Organize content with H2/H3 headings as needed; do not rely on external grouping options.
- For each key development: what happened, why it matters, timeline/context, and cite key sources with links.
- Keep the tone factual; avoid speculation; conclude with concise takeaways.
- Return ONLY the Markdown content.`, topicName, summary, detailed, highlights, sources)

    out, err := llm.Generate(ctx, prompt, model, map[string]interface{}{"temperature": 0.3})
    if err != nil {
        return "", err
    }

    final, err := ExtractMarkdown(out)
	if err != nil {
		return out, nil
	}
	return final, nil
}

func writeRunDeepDive(topicID, runID, md string) error {
	dir := filepath.Join("runs", sanitize(topicID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sanitize(runID)+"_deepdive.md"), []byte(md), 0o644)
}

// markdown returns the stored/generated markdown artifact for a specific run
//
//	@Summary   Run markdown by ID
//	@Tags      runs
//	@Security  BearerAuth
//	@Security  CookieAuth
//	@Param     topic_id  path  string  true  "Topic ID"
//	@Param     run_id    path  string  true  "Run ID"
//	@Produce   text/markdown
//	@Success   200  {string}  string
//	@Failure   404  {object}  HTTPError
//	@Router    /api/topics/{topic_id}/runs/{run_id}/markdown [get]
func (h *RunsHandler) markdown(c echo.Context) error {
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	// Ensure topic belongs to user
	name, _, _, err := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	// Try existing file first
	if b, err := os.ReadFile(runMarkdownPath(topicID, runID)); err == nil && len(b) > 0 {
		return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", b)
	}
	// Generate from stored result
	res, err := h.store.GetProcessingResultByID(c.Request().Context(), runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	md := renderMarkdownReport(name, res)
	if md == "" {
		return echo.NewHTTPError(http.StatusNotFound, "no markdown available")
	}
	_ = writeRunMarkdown(topicID, runID, md)
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}

// writeRunMarkdown persists the markdown artifact under ./runs/<topic_id>/<run_id>.md
func writeRunMarkdown(topicID, runID, md string) error {
	dir := filepath.Join("runs", sanitize(topicID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sanitize(runID)+".md"), []byte(md), 0o644)
}

func runMarkdownPath(topicID, runID string) string {
	return filepath.Join("runs", sanitize(topicID), sanitize(runID)+".md")
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	if s == "" {
		s = "_"
	}
	return s
}

// renderMarkdownReport converts a processing result JSON to a readable, categorized markdown document
func renderMarkdownReport(topicName string, res map[string]interface{}) string {
	var b strings.Builder
	// Header
	now := time.Now().Format(time.RFC3339)
	fmt.Fprintf(&b, "# %s — News Brief\n\n", safeString(topicName))
	fmt.Fprintf(&b, "_Generated: %s_\n\n", now)

	// Summary snapshot
	summary := safeString(res["summary"])
	confidence := safeFloat(res["confidence"])
	tokens := safeInt(res["tokens_used"]) // might be 0 if absent
	cost := safeFloat(res["cost_estimate"])
	fmt.Fprintf(&b, "## Executive Summary\n\n")
	if summary != "" {
		fmt.Fprintf(&b, "%s\n\n", summary)
	} else {
		fmt.Fprintf(&b, "No summary available.\n\n")
	}
	if confidence > 0 {
		fmt.Fprintf(&b, "- Confidence: %.2f\n", confidence)
	}
	if cost > 0 || tokens > 0 {
		fmt.Fprintf(&b, "- Cost/Tokens: $%.2f / %d tokens\n", cost, tokens)
	}
	b.WriteString("\n")

	// Highlights
	if hs, ok := res["highlights"].([]interface{}); ok && len(hs) > 0 {
		b.WriteString("## Key Developments\n\n")
		for _, it := range hs {
			if m, ok := it.(map[string]interface{}); ok {
				title := safeString(m["title"])
				content := safeString(m["content"])
				if title == "" && content == "" {
					continue
				}
				if title != "" {
					fmt.Fprintf(&b, "- **%s** — %s\n", title, content)
				} else {
					fmt.Fprintf(&b, "- %s\n", content)
				}
			}
		}
		b.WriteString("\n")
	}

	// Detailed report
	detailed := safeString(res["detailed_report"])
	if detailed != "" {
		b.WriteString("## Detailed Report\n\n")
		b.WriteString(detailed)
		if !strings.HasSuffix(detailed, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Knowledge graph summary (counts)
	if meta, ok := res["metadata"].(map[string]interface{}); ok {
		if kg, ok := meta["knowledge_graph"].(map[string]interface{}); ok {
			var nCount, eCount int
			if ns, ok := kg["nodes"].([]interface{}); ok {
				nCount = len(ns)
			}
			if es, ok := kg["edges"].([]interface{}); ok {
				eCount = len(es)
			}
			if nCount > 0 || eCount > 0 {
				b.WriteString("## Knowledge Graph Overview\n\n")
				fmt.Fprintf(&b, "- Nodes: %d\n- Edges: %d\n\n", nCount, eCount)
			}
		}
	}

	// Sources grouped by domain
	if ss, ok := res["sources"].([]interface{}); ok && len(ss) > 0 {
		b.WriteString("## Sources\n\n")
		grouped := map[string][]string{}
		for _, it := range ss {
			if m, ok := it.(map[string]interface{}); ok {
				url := safeString(m["url"])
				title := safeString(m["title"])
				if url == "" && title == "" {
					continue
				}
				dom := domainOf(url)
				item := fmt.Sprintf("- [%s](%s)", or(title, url), or(url, "#"))
				grouped[dom] = append(grouped[dom], item)
			}
		}
		// stable order by domain
		var keys []string
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k != "" {
				fmt.Fprintf(&b, "### %s\n\n", k)
			}
			for _, line := range grouped[k] {
				b.WriteString(line + "\n")
			}
			b.WriteString("\n")
		}
	}

	// Footer
	b.WriteString("---\n")
	b.WriteString("This report is auto-generated based on your topic and preferences.\n")
	return b.String()
}

func safeString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	b, _ := json.Marshal(v)
	return strings.TrimSpace(string(b))
}
func safeFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}
func safeInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}
func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func domainOf(url string) string {
	if url == "" {
		return ""
	}
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
