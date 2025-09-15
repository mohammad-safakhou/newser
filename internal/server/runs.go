package server

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"

	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/helpers"
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
    g.GET("/:topic_id/runs/:run_id/html", h.html)
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

	out, err := llm.Generate(c.Request().Context(), prompt, model, nil)
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

// renderHTMLReport produces a minimal Tailwind-styled HTML report with collapsible sections.
func renderHTMLReport(topic string, res map[string]interface{}) string {
    var b strings.Builder
    b.WriteString("<!doctype html><html lang=\"en\" dir=\"auto\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
    // Note: Tailwind CSS is not embedded; class names are included so the web app can style if served there.
    b.WriteString("<title>Report — " + templateEscape(topic) + "</title>")
    b.WriteString("</head><body class=\"bg-slate-950 text-slate-100 p-6\">")
    b.WriteString("<a class=\"sr-only focus:not-sr-only text-xs text-sky-400\" href=\"#main\">Skip to content</a>")
    b.WriteString("<div id=\"main\" tabindex=\"-1\" class=\"max-w-4xl mx-auto space-y-6\">")
    b.WriteString("<header class=\"space-y-1\"><h1 class=\"text-xl font-semibold\">" + templateEscape(topic) + " — Report</h1>")
    if t, ok := res["created_at"].(string); ok && t != "" { b.WriteString("<div class=\"text-xs text-slate-400\">Generated at " + templateEscape(t) + "</div>") }
    b.WriteString("</header>")

    // Executive summary
    summary := templateEscape(safeString(res["summary"]))
    if summary != "" {
        b.WriteString("<section><h2 class=\"text-sm font-semibold mb-2\">Executive Summary</h2><p class=\"text-sm\">" + summary + "</p></section>")
    }

    // Items grouped by category with collapsible details
    items, _ := res["metadata"].(map[string]interface{})
    var arr []interface{}
    if items != nil { if it, ok := items["items"].([]interface{}); ok { arr = it } }
    if len(arr) > 0 {
        // bucketize
        buckets := map[string][]map[string]interface{}{}
        for _, it := range arr {
            if m, ok := it.(map[string]interface{}); ok {
                c := normCategory(strings.ToLower(safeString(m["category"])) )
                buckets[c] = append(buckets[c], m)
            }
        }
        order := []string{"top", "policy", "politics", "legal", "markets", "other"}
        totalCount := len(arr)
        printed := 0
        const maxItems = 12
        stopped := false
        for _, cat := range order {
            list := buckets[cat]
            if len(list) == 0 { continue }
            b.WriteString("<section class=\"space-y-2\"><h3 class=\"text-sm font-semibold\">" + templateEscape(strings.Title(cat)) + "</h3>")
            for _, m := range list {
                if printed >= maxItems { stopped = true; break }
                title := templateEscape(safeString(m["title"])) ; if title == "" { title = "Untitled" }
                sum := templateEscape(safeString(m["summary"]))
                conf, _ := m["confidence"].(float64)
                low := conf > 0 && conf < 0.6
                badge := ""
                if low { badge = "<span class=\"text-amber-400\">⚠️</span> " }
                b.WriteString("<details class=\"rounded border border-slate-800 bg-slate-900/40\"><summary class=\"px-3 py-2 cursor-pointer\">" + badge + title + "</summary>")
                b.WriteString("<div class=\"px-3 pb-3 text-sm space-y-2\">")
                if sum != "" { b.WriteString("<p>" + sum + "</p>") }
                // meta line
                meta := []string{}
                if t := safeString(m["published_at"]); t != "" {
                    if tt, err := time.Parse(time.RFC3339, t); err == nil {
                        meta = append(meta, templateEscape("🕒 "+tt.Format(time.RFC3339)+" ("+rel(tt)+")"))
                    } else {
                        meta = append(meta, templateEscape("🕒 "+t))
                    }
                }
                if s := safeString(m["seen_at"]); s != "" { meta = append(meta, "👁️ "+templateEscape(s)) }
                if conf > 0 { meta = append(meta, fmt.Sprintf("Confidence: %.2f", conf)) }
                if imp, ok := m["importance"].(float64); ok && imp > 0 { meta = append(meta, fmt.Sprintf("Importance: %.2f", imp)) }
                if len(meta) > 0 { b.WriteString("<div class=\"text-xs text-slate-400\">" + templateEscape(strings.Join(meta, " · ")) + "</div>") }
                // sources
                if srcs, ok := m["sources"].([]interface{}); ok && len(srcs) > 0 {
                    b.WriteString("<div class=\"mt-2\"><div class=\"text-xs text-slate-400\">Sources</div><ul class=\"text-xs list-disc pl-5\">")
                    for _, s := range srcs {
                        if sm, ok := s.(map[string]interface{}); ok {
                            url := templateEscape(safeString(sm["url"]))
                            dom := templateEscape(safeString(sm["domain"]))
                            arch := templateEscape(safeString(sm["archived_url"]))
                            if url != "" {
                                label := dom ; if label == "" { label = url }
                                if strings.Contains(strings.ToLower(label), "wikipedia.org") { label += " (background)" }
                                b.WriteString("<li><a class=\"text-sky-400 hover:underline\" href=\"" + url + "\" target=\"_blank\" rel=\"noopener\">" + label + "</a>")
                                if arch != "" { b.WriteString(" <span class=\"text-slate-500\">(archived: <a class=\"hover:underline\" href=\""+arch+"\" target=\"_blank\" rel=\"noopener\">"+templateEscape(shortDomain(arch))+"</a>)</span>") }
                                b.WriteString("</li>")
                            }
                        }
                    }
                    b.WriteString("</ul><div class=\"pt-1\"><a class=\"text-xs text-sky-400 hover:underline\" href=\"#main\">Back to top</a></div></div>")
                }
                b.WriteString("</div></details>")
                printed++
            }
            b.WriteString("</section>")
            if stopped { break }
        }
        if stopped && totalCount > printed {
            b.WriteString("<div class=\"text-xs text-slate-400\">More items available: showing "+fmt.Sprintf("%d of %d", printed, totalCount)+".</div>")
        }
    }

    // Detailed report collapsible
    detailed := safeString(res["detailed_report"])
    if detailed != "" {
        b.WriteString("<section><details class=\"rounded border border-slate-800 bg-slate-900/40\"><summary class=\"px-3 py-2 cursor-pointer\">Detailed Report</summary><div class=\"px-3 pb-3 prose prose-invert max-w-none\">")
        b.WriteString(templateEscape(detailed))
        b.WriteString("</div></details></section>")
    }

    b.WriteString("</div></body></html>")
    return b.String()
}

func templateEscape(s string) string {
    r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
    return r.Replace(s)
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

// HTML view for a run's report (feature-flagged optional view). Returns text/html.
//
//  @Summary  Run report as HTML
//  @Tags     runs
//  @Security BearerAuth
//  @Security CookieAuth
//  @Param    topic_id path string true "Topic ID"
//  @Param    run_id   path string true "Run ID"
//  @Produce  html
//  @Success  200 string string
//  @Failure  404 {object} HTTPError
//  @Router   /api/topics/{topic_id}/runs/{run_id}/html [get]
func (h *RunsHandler) html(c echo.Context) error {
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
    name, _, _, _ := h.store.GetTopicByID(c.Request().Context(), topicID, userID)
    html := renderHTMLReport(name, res)
    if html == "" { return echo.NewHTTPError(http.StatusNotFound, "no report available") }
    return c.HTML(http.StatusOK, html)
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
						if u, ok := m["url"].(string); ok && u != "" {
							known = append(known, u)
						}
					}
				}
			}
			if len(known) > 0 {
				ctxMap["known_urls"] = known
			}
		}
	}
	if kg, err := st.GetKnowledgeGraph(ctx, topicID); err == nil {
		ctxMap["knowledge_graph"] = map[string]interface{}{"nodes": kg.Nodes, "edges": kg.Edges, "last_updated": kg.LastUpdated}
	}

	// Fetch topic (name, preferences)
	name, prefsB, _, err := st.GetTopicByID(ctx, topicID, userID)
	if err != nil {
		return err
	}
	var prefs Preferences
	_ = json.Unmarshal(prefsB, &prefs)

	// Construct thought from preferences (not user-defined name)
	content := deriveThoughtContentFromPrefs(prefs)
	thought := core.UserThought{ID: runID, Content: content, Preferences: prefs, Timestamp: time.Now(), Context: ctxMap}

	result, err := orch.ProcessThought(ctx, thought)
	if err != nil {
		return err
	}

	// Persist
	_ = st.UpsertProcessingResult(ctx, result)
	if len(result.Highlights) > 0 {
		_ = st.SaveHighlights(ctx, topicID, result.Highlights)
	}
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
	if model == "" {
		model = cfg.LLM.Routing.Chatting
	}

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

	out, err := llm.Generate(ctx, prompt, model, nil)
	if err != nil {
		return "", err
	}

	final, err := helpers.ExtractMarkdown(out)
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

    // Itemized digest if available
    if md, ok := res["metadata"].(map[string]interface{}); ok {
        if items, ok := md["items"].([]interface{}); ok && len(items) > 0 {
            // Quick Stats
            fmt.Fprintf(&b, "## Quick Stats\n\n")
            if stats, ok := md["digest_stats"].(map[string]interface{}); ok {
                if c, ok := stats["count"].(int); ok { fmt.Fprintf(&b, "- Items: %d\n", c) }
                if c, ok := asIntIface(stats["count"]); ok { fmt.Fprintf(&b, "- Items: %d\n", c) }
                if cats, ok := stats["categories"].(map[string]interface{}); ok {
                    var lines []string
                    for k, v := range cats { if n, ok := asIntIface(v); ok { lines = append(lines, fmt.Sprintf("%s: %d", k, n)) } }
                    sort.Strings(lines)
                    if len(lines) > 0 { fmt.Fprintf(&b, "- Categories: %s\n", strings.Join(lines, ", ")) }
                }
                if span, ok := stats["span_hours"].(float64); ok { fmt.Fprintf(&b, "- Span: %.0fh\n", span) }
                if top, ok := stats["top_domains"].([]interface{}); ok {
                    var ds []string
                    for _, d := range top { if s, ok := d.(string); ok { ds = append(ds, s) } }
                    if len(ds) > 0 { fmt.Fprintf(&b, "- Top sources: %s\n", strings.Join(ds, ", ")) }
                }
                b.WriteString("\n")
            }
            // Table of contents
            fmt.Fprintf(&b, "## Contents\n\n")
            for i, it := range items {
                if m, ok := it.(map[string]interface{}); ok {
                    title := safeString(m["title"]) ; if title == "" { title = fmt.Sprintf("Item %d", i+1) }
                    anchor := slugify(title)
                    fmt.Fprintf(&b, "- [%s](#%s)\n", title, anchor)
                }
            }
            b.WriteString("\n")
            // Items grouped by category (hard cap with pointer to more)
            fmt.Fprintf(&b, "## Today’s Items\n\n")
            // bucketize by normalized category
            buckets := map[string][]map[string]interface{}{}
            for _, it := range items {
                if m, ok := it.(map[string]interface{}); ok {
                    c := normCategory(strings.ToLower(safeString(m["category"])))
                    buckets[c] = append(buckets[c], m)
                }
            }
            order := []string{"top", "policy", "politics", "legal", "markets", "other"}
            totalCount := len(items)
            printed := 0
            const maxItems = 12
            stopped := false
            for _, cat := range order {
                arr := buckets[cat]
                if len(arr) == 0 { continue }
                // category header
                fmt.Fprintf(&b, "### %s %s\n\n", badgeFor(cat), strings.Title(cat))
                for _, m := range arr {
                    if printed >= maxItems { stopped = true; break }
                    title := safeString(m["title"]) ; if title == "" { title = "Untitled" }
                    anchor := slugify(title)
                    // confidence badge if low
                    lowConf := false
                    if conf, ok := m["confidence"].(float64); ok && conf > 0 && conf < 0.6 { lowConf = true }
                    header := title
                    if lowConf { header = "⚠️ " + header }
                    // item header
                    fmt.Fprintf(&b, "#### %s\n\n", header)
                    fmt.Fprintf(&b, "<a id=\"%s\"></a>\n\n", anchor)
                    sum := safeString(m["summary"]) ; if sum != "" { fmt.Fprintf(&b, "%s\n\n", sum) }
                    meta := []string{}
                    if t := safeString(m["published_at"]); t != "" {
                        if tt, err := time.Parse(time.RFC3339, t); err == nil {
                            meta = append(meta, fmt.Sprintf("🕒 %s (%s)", tt.Format(time.RFC3339), rel(tt)))
                        } else {
                            meta = append(meta, fmt.Sprintf("🕒 %s", t))
                        }
                    }
                    if s := safeString(m["seen_at"]); s != "" { meta = append(meta, fmt.Sprintf("👁️ %s", s)) }
                    if conf, ok := m["confidence"].(float64); ok && conf > 0 { meta = append(meta, fmt.Sprintf("Confidence: %.2f", conf)) }
                    if imp, ok := m["importance"].(float64); ok && imp > 0 { meta = append(meta, fmt.Sprintf("Importance: %.2f", imp)) }
                    if len(meta) > 0 { fmt.Fprintf(&b, "_Meta_: %s\n\n", strings.Join(meta, " · ")) }
                    if lowConf {
                        b.WriteString("> Note: This item has low confidence; consider verifying with additional sources.\n\n")
                    }
                    if srcs, ok := m["sources"].([]interface{}); ok && len(srcs) > 0 {
                        // Reorder to put non-wikipedia first
                        var list []map[string]interface{}
                        for _, s := range srcs { if sm, ok := s.(map[string]interface{}); ok { list = append(list, sm) } }
                        sort.SliceStable(list, func(i, j int) bool {
                            di := strings.ToLower(safeString(list[i]["domain"]))
                            dj := strings.ToLower(safeString(list[j]["domain"]))
                            wi := strings.Contains(di, "wikipedia.org")
                            wj := strings.Contains(dj, "wikipedia.org")
                            if wi == wj { return di < dj }
                            return !wi && wj
                        })
                        b.WriteString("Sources:\n\n")
                        for _, sm := range list {
                            url := safeString(sm["url"]) ; dom := safeString(sm["domain"]) ; arch := safeString(sm["archived_url"]) ;
                            label := dom ; if label == "" { label = url }
                            if strings.Contains(strings.ToLower(dom), "wikipedia.org") { label += " (background)" }
                            if url != "" {
                                fmt.Fprintf(&b, "- [%s](%s)", label, url)
                                if arch != "" { fmt.Fprintf(&b, " (archived: [%s](%s))", shortDomain(arch), arch) }
                                b.WriteString("\n")
                            }
                        }
                        b.WriteString("\n")
                    }
                    printed++
                }
                b.WriteString("\n")
                if stopped { break }
            }
            if stopped && totalCount > printed {
                fmt.Fprintf(&b, "_More items available: showing %d of %d._\n\n", printed, totalCount)
            }
            // Source appendix
            fmt.Fprintf(&b, "## Source List (Appendix)\n\n")
            uniq := map[string]bool{}
            for _, it := range items {
                if m, ok := it.(map[string]interface{}); ok {
                    pub := safeString(m["published_at"]) ; if pub == "" { pub = "N/A" }
                    if srcs, ok := m["sources"].([]interface{}); ok {
                        for _, s := range srcs {
                            if sm, ok := s.(map[string]interface{}); ok {
                                url := safeString(sm["url"]) ; if url == "" { continue }
                                if uniq[url] { continue }
                                uniq[url] = true
                                dom := safeString(sm["domain"]) ; if dom == "" { dom = shortDomain(url) }
                                fmt.Fprintf(&b, "- %s — [%s](%s)\n", pub, dom, url)
                            }
                        }
                    }
                }
            }
            b.WriteString("\n")
        }
    }
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
    // Metadata footer (avoid redeclaring tokens/cost; we used them above in summary)
    if t, ok := res["created_at"].(string); ok && t != "" {
        fmt.Fprintf(&b, "Generated at: %s\n", t)
    }
    // reuse tokens/cost computed in Executive Summary section
    if tokens > 0 || cost > 0 {
        fmt.Fprintf(&b, "Tokens: %d", tokens)
        if cost > 0 { fmt.Fprintf(&b, "; Cost: $%.4f", cost) }
        b.WriteString("\n")
    }
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
func asIntIface(v interface{}) (int, bool) {
    switch t := v.(type) {
    case int:
        return t, true
    case int64:
        return int(t), true
    case float64:
        return int(t), true
    default:
        return 0, false
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
func shortDomain(u string) string { return domainOf(u) }
func slugify(s string) string {
    s = strings.ToLower(strings.TrimSpace(s))
    s = strings.ReplaceAll(s, " ", "-")
    s = strings.ReplaceAll(s, "/", "-")
    s = strings.ReplaceAll(s, "#", "-")
    return s
}
func badgeFor(cat string) string {
    switch cat {
    case "top", "breaking":
        return "🔴"
    case "policy":
        return "🏛️"
    case "politics":
        return "🗳️"
    case "legal", "regulatory":
        return "⚖️"
    case "markets", "economy":
        return "📈"
    default:
        return "•"
    }
}
func normCategory(c string) string {
    switch c {
    case "top", "breaking":
        return "top"
    case "policy":
        return "policy"
    case "politics":
        return "politics"
    case "legal", "regulatory":
        return "legal"
    case "markets", "economy":
        return "markets"
    default:
        return "other"
    }
}

// rel returns a rough relative time like "2h ago".
func rel(t time.Time) string {
    d := time.Since(t)
    if d < time.Minute { return "just now" }
    if d < time.Hour { return fmt.Sprintf("%dm ago", int(d.Minutes())) }
    if d < 24*time.Hour { return fmt.Sprintf("%dh ago", int(d.Hours())) }
    days := int(d.Hours())/24
    if days < 30 { return fmt.Sprintf("%dd ago", days) }
    months := days/30
    if months < 12 { return fmt.Sprintf("%dmo ago", months) }
    years := months/12
    return fmt.Sprintf("%dy ago", years)
}
