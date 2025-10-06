package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

        "github.com/labstack/echo/v4"
        "github.com/mohammad-safakhou/newser/config"
        core "github.com/mohammad-safakhou/newser/internal/agent/core"
        "github.com/mohammad-safakhou/newser/internal/budget"
        "github.com/mohammad-safakhou/newser/internal/helpers"
	"github.com/mohammad-safakhou/newser/internal/manifest"
	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/planner"
	policy "github.com/mohammad-safakhou/newser/internal/policy"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type RunsHandler struct {
	store  *store.Store
	orch   *core.Orchestrator
	cfg    *config.Config
	sem    *semantic.Ingestor
	embed  core.LLMProvider
	logger *log.Logger
}

type episodeResponse struct {
	RunID        string                `json:"run_id"`
	TopicID      string                `json:"topic_id"`
	Thought      core.UserThought      `json:"thought"`
	PlanDocument *planner.PlanDocument `json:"plan_document,omitempty"`
	PlanRaw      json.RawMessage       `json:"plan_raw,omitempty"`
	PlanPrompt   string                `json:"plan_prompt,omitempty"`
	Result       core.ProcessingResult `json:"result"`
	Steps        []episodeStepResponse `json:"steps"`
	CreatedAt    time.Time             `json:"created_at"`
}

type episodeStepResponse struct {
	StepIndex     int                      `json:"step_index"`
	Task          core.AgentTask           `json:"task"`
	InputSnapshot map[string]interface{}   `json:"input_snapshot,omitempty"`
	Prompt        string                   `json:"prompt,omitempty"`
	Result        core.AgentResult         `json:"result"`
	Artifacts     []map[string]interface{} `json:"artifacts,omitempty"`
	StartedAt     *time.Time               `json:"started_at,omitempty"`
	CompletedAt   *time.Time               `json:"completed_at,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
}

type planExplainResponse struct {
	PlanID         string                `json:"plan_id"`
	ThoughtID      string                `json:"thought_id"`
	Version        string                `json:"version"`
	Confidence     float64               `json:"confidence"`
	ExecutionOrder []string              `json:"execution_order,omitempty"`
	UpdatedAt      time.Time             `json:"updated_at"`
	Plan           *planner.PlanDocument `json:"plan,omitempty"`
	Raw            json.RawMessage       `json:"raw_plan,omitempty"`
}

type memoryHit struct {
	RunID      string                 `json:"run_id"`
	TopicID    string                 `json:"topic_id"`
	TaskID     string                 `json:"task_id,omitempty"`
	Kind       string                 `json:"kind"`
	Distance   float64                `json:"distance"`
	Similarity float64                `json:"similarity"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

type memoryHitsResponse struct {
	Query           string      `json:"query"`
	TopK            int         `json:"top_k"`
	Threshold       float64     `json:"threshold"`
	RunMatches      []memoryHit `json:"run_matches,omitempty"`
	PlanStepMatches []memoryHit `json:"plan_step_matches,omitempty"`
}

type runStreamItem struct {
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	CostEstimate *float64   `json:"cost_estimate,omitempty"`
}

type runStreamPayload struct {
	TopicID           string          `json:"topic_id"`
	GeneratedAt       time.Time       `json:"generated_at"`
	IntervalSeconds   int             `json:"interval_seconds"`
	Runs              []runStreamItem `json:"runs"`
	TotalCostEstimate float64         `json:"total_cost_estimate"`
}

var (
        runsTracer = otel.Tracer("newser/internal/server/runs")
)

func NewRunsHandler(cfg *config.Config, store *store.Store, orch *core.Orchestrator, provider core.LLMProvider, ingest *semantic.Ingestor) *RunsHandler {
	logger := log.New(log.Writer(), "[RUNS] ", log.LstdFlags)
	if cfg != nil && cfg.Memory.Semantic.Enabled && provider == nil {
		logger.Printf("warn: semantic memory enabled but embedding provider not configured")
	}
	return &RunsHandler{
		store:  store,
		orch:   orch,
		cfg:    cfg,
		sem:    ingest,
		embed:  provider,
		logger: logger,
	}
}

func (h *RunsHandler) Register(g *echo.Group, secret []byte) {
	g.Use(runtime.EchoAuthMiddleware(secret))
	g.POST("/:topic_id/trigger", h.trigger)
	g.GET("/:topic_id/runs", h.list)
	g.GET("/:topic_id/latest_result", h.latest)
	g.GET("/:topic_id/runs/:run_id/result", h.result)
	g.POST("/:topic_id/runs/:run_id/expand", h.expand)
	g.GET("/:topic_id/runs/:run_id/markdown", h.markdown)
	g.POST("/:topic_id/runs/:run_id/expand_all", h.expandAll)
	g.GET("/:topic_id/runs/:run_id/html", h.html)
	g.GET("/:topic_id/runs/:run_id/episode", h.episode)
	g.POST("/:topic_id/runs/:run_id/budget_decision", h.budgetDecision)
	g.GET("/:topic_id/runs/:run_id/plan", h.planExplain)
	g.GET("/:topic_id/runs/:run_id/memory_hits", h.memoryHits)
	if h.cfg == nil || h.cfg.Server.RunStreamEnabled {
		g.GET("/:topic_id/runs/stream", h.streamRuns)
	}
	if h.cfg == nil || h.cfg.Server.RunManifestEnabled {
		g.POST("/:topic_id/runs/:run_id/manifest", h.createManifest)
		g.GET("/:topic_id/runs/:run_id/manifest", h.fetchManifest)
	}
}

func newEpisodeResponse(ep store.Episode) episodeResponse {
	resp := episodeResponse{
		RunID:        ep.RunID,
		TopicID:      ep.TopicID,
		Thought:      ep.Thought,
		PlanDocument: ep.PlanDocument,
		PlanPrompt:   ep.PlanPrompt,
		Result:       ep.Result,
		CreatedAt:    ep.CreatedAt,
	}
	if len(ep.PlanRaw) > 0 {
		resp.PlanRaw = append(json.RawMessage{}, ep.PlanRaw...)
	}
	if len(ep.Steps) > 0 {
		resp.Steps = make([]episodeStepResponse, len(ep.Steps))
		for i, step := range ep.Steps {
			resp.Steps[i] = episodeStepResponse{
				StepIndex:     step.StepIndex,
				Task:          step.Task,
				InputSnapshot: step.InputSnapshot,
				Prompt:        step.Prompt,
				Result:        step.Result,
				Artifacts:     step.Artifacts,
				CreatedAt:     step.CreatedAt,
			}
			if step.StartedAt != nil {
				resp.Steps[i].StartedAt = step.StartedAt
			}
			if step.CompletedAt != nil {
				resp.Steps[i].CompletedAt = step.CompletedAt
			}
		}
	}
	return resp
}

// episode returns the stored episodic snapshot for a completed run
//
//	@Summary	Run episode snapshot
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		run_id		path	string	true	"Run ID"
//	@Produce	json
//	@Success	200	{object}	episodeResponse
//	@Failure	403	{object}	HTTPError
//	@Failure	404	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/{run_id}/episode [get]
func (h *RunsHandler) episode(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	ep, ok, err := h.store.GetEpisodeByRunID(ctx, runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "episode not found")
	}
	if ep.TopicID != topicID {
		return echo.NewHTTPError(http.StatusForbidden, "episode does not belong to topic")
	}
	resp := newEpisodeResponse(ep)
	return c.JSON(http.StatusOK, resp)
}

// planExplain returns the stored planner graph for a run thought.
//
//	@Summary	Run plan graph
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		run_id		path	string	true	"Run ID"
//	@Produce	json
//	@Success	200	{object}	planExplainResponse
//	@Failure	404	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/{run_id}/plan [get]
func (h *RunsHandler) planExplain(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	rec, ok, err := h.store.GetLatestPlanGraph(ctx, runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "plan graph not found")
	}
	if rec.ThoughtID != "" && rec.ThoughtID != runID {
		return echo.NewHTTPError(http.StatusForbidden, "plan graph does not belong to run")
	}
	var doc planner.PlanDocument
	if err := json.Unmarshal(rec.PlanJSON, &doc); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("decode plan document: %v", err))
	}
	if doc.PlanID == "" {
		doc.PlanID = rec.PlanID
	}
	resp := planExplainResponse{
		PlanID:         rec.PlanID,
		ThoughtID:      rec.ThoughtID,
		Version:        rec.Version,
		Confidence:     rec.Confidence,
		ExecutionOrder: rec.ExecutionOrder,
		UpdatedAt:      rec.UpdatedAt,
		Plan:           &doc,
		Raw:            append(json.RawMessage{}, rec.PlanJSON...),
	}
	return c.JSON(http.StatusOK, resp)
}

// memoryHits returns semantic memory matches relevant to a run summary.
//
//	@Summary	Run memory hits
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		run_id		path	string	true	"Run ID"
//	@Param		q		query	string	false	"Override query text"
//	@Param		top_k	query	int	false	"Maximum matches to return"
//	@Param		threshold	query	number	false	"Maximum embedding distance"
//	@Param		include_steps	query	bool	false	"Include plan step matches"
//	@Produce	json
//	@Success	200	{object}	memoryHitsResponse
//	@Failure	400	{object}	HTTPError
//	@Failure	404	{object}	HTTPError
//	@Failure	503	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/{run_id}/memory_hits [get]
func (h *RunsHandler) memoryHits(c echo.Context) error {
	ctx := c.Request().Context()
	if h.cfg == nil || !h.cfg.Memory.Semantic.Enabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "semantic memory disabled")
	}
	if h.embed == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "embedding provider unavailable")
	}
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	runID := c.Param("run_id")
	res, err := h.store.GetProcessingResultByID(ctx, runID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	query := strings.TrimSpace(c.QueryParam("q"))
	if query == "" {
		if summary, ok := res["summary"].(string); ok {
			query = strings.TrimSpace(summary)
		}
	}
	if query == "" {
		if detailed, ok := res["detailed_report"].(string); ok {
			query = strings.TrimSpace(detailed)
		}
	}
	if query == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter or run summary required")
	}
	topK := h.cfg.Memory.Semantic.SearchTopK
	if topK <= 0 {
		topK = 5
	}
	if tk := strings.TrimSpace(c.QueryParam("top_k")); tk != "" {
		if v, err := strconv.Atoi(tk); err == nil && v > 0 {
			topK = v
		}
	}
	threshold := h.cfg.Memory.Semantic.SearchThreshold
	if th := strings.TrimSpace(c.QueryParam("threshold")); th != "" {
		if v, err := strconv.ParseFloat(th, 64); err == nil && v > 0 {
			threshold = v
		}
	}
	includeSteps := true
	if inc := strings.TrimSpace(c.QueryParam("include_steps")); inc != "" {
		if v, err := strconv.ParseBool(inc); err == nil {
			includeSteps = v
		}
	}
	model := h.cfg.Memory.Semantic.EmbeddingModel
	if model == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "semantic embedding model not configured")
	}
	vectors, err := h.embed.Embed(ctx, model, []string{query})
	if err != nil {
		h.logger.Printf("memoryHits: embed failed: %v", err)
		return echo.NewHTTPError(http.StatusBadGateway, "failed to embed query")
	}
	if len(vectors) == 0 {
		return c.JSON(http.StatusOK, memoryHitsResponse{Query: query, TopK: topK, Threshold: threshold})
	}
	resp := memoryHitsResponse{Query: query, TopK: topK, Threshold: threshold}
	vector := vectors[0]
	runMatches, err := h.store.SearchRunEmbeddings(ctx, topicID, vector, topK, threshold)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, hit := range runMatches {
		if hit.RunID == runID {
			continue
		}
		resp.RunMatches = append(resp.RunMatches, memoryHit{
			RunID:      hit.RunID,
			TopicID:    hit.TopicID,
			Kind:       hit.Kind,
			Distance:   hit.Distance,
			Similarity: clampSimilarity(1 - hit.Distance),
			Metadata:   hit.Metadata,
			CreatedAt:  hit.CreatedAt,
		})
	}
	if includeSteps {
		stepMatches, err := h.store.SearchPlanStepEmbeddings(ctx, topicID, vector, topK, threshold)
		if err != nil {
			h.logger.Printf("memoryHits: plan step search failed: %v", err)
		} else {
			for _, hit := range stepMatches {
				resp.PlanStepMatches = append(resp.PlanStepMatches, memoryHit{
					RunID:      hit.RunID,
					TopicID:    hit.TopicID,
					TaskID:     hit.TaskID,
					Kind:       hit.Kind,
					Distance:   hit.Distance,
					Similarity: clampSimilarity(1 - hit.Distance),
					Metadata:   hit.Metadata,
					CreatedAt:  hit.CreatedAt,
				})
			}
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// streamRuns streams run status and cost metrics via Server-Sent Events.
//
//	@Summary	Run metrics stream
//	@Tags		runs
//	@Security	BearerAuth
//	@Security	CookieAuth
//	@Param		topic_id	path	string	true	"Topic ID"
//	@Param		interval	query	int	false	"Refresh cadence in seconds (default 5)"
//	@Produce	text/event-stream
//	@Success	200	{string}	string
//	@Failure	400	{object}	HTTPError
//	@Failure	404	{object}	HTTPError
//	@Failure	503	{object}	HTTPError
//	@Router		/api/topics/{topic_id}/runs/stream [get]
func (h *RunsHandler) streamRuns(c echo.Context) error {
	if h.cfg != nil && !h.cfg.Server.RunStreamEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "run stream disabled")
	}
	req := c.Request()
	ctx := req.Context()
	topicID := c.Param("topic_id")
	ctx, span := runsTracer.Start(ctx, "RunsHandler.streamRuns")
	defer span.End()
	span.SetAttributes(attribute.String("topic_id", topicID))
	c.SetRequest(req.WithContext(ctx))
	userID, _ := c.Get("user_id").(string)
	if strings.TrimSpace(topicID) == "" {
		span.SetStatus(codes.Error, "topic_id required")
		return echo.NewHTTPError(http.StatusBadRequest, "topic_id required")
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	interval := 5 * time.Second
	if val := strings.TrimSpace(c.QueryParam("interval")); val != "" {
		if sec, err := strconv.Atoi(val); err == nil && sec > 0 {
			interval = time.Duration(sec) * time.Second
		}
	}
	span.SetAttributes(attribute.Int("interval_seconds", int(interval/time.Second)))
	resp := c.Response()
	resp.Header().Set(echo.HeaderContentType, "text/event-stream")
	resp.Header().Set(echo.HeaderCacheControl, "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.WriteHeader(http.StatusOK)
	flusher, ok := resp.Writer.(http.Flusher)
	if !ok {
		span.SetStatus(codes.Error, "streaming unsupported")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "streaming unsupported")
	}

	sendSnapshot := func() error {
		runs, err := h.store.ListRuns(ctx, topicID)
		if err != nil {
			trace.SpanFromContext(ctx).RecordError(err)
			return err
		}
		payload := runStreamPayload{
			TopicID:         topicID,
			GeneratedAt:     time.Now().UTC(),
			IntervalSeconds: int(interval / time.Second),
		}
		maxRuns := 25
		for idx, run := range runs {
			if idx >= maxRuns {
				break
			}
			item := runStreamItem{
				RunID:      run.ID,
				Status:     run.Status,
				StartedAt:  run.StartedAt,
				FinishedAt: run.FinishedAt,
			}
			if run.Status == "succeeded" || run.Status == "running" {
				if pr, err := h.store.GetProcessingResultByID(ctx, run.ID); err == nil {
					if cost, ok := extractCostEstimate(pr["cost_estimate"]); ok {
						payload.TotalCostEstimate += cost
						item.CostEstimate = &cost
					}
				}
			}
			payload.Runs = append(payload.Runs, item)
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := resp.Write([]byte("event: update\n")); err != nil {
			return err
		}
		if _, err := resp.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := sendSnapshot(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := sendSnapshot(); err != nil {
				span.RecordError(err)
				h.logger.Printf("runs stream snapshot failed: %v", err)
			}
		}
	}
}

// createManifest generates, signs, and persists a run manifest.
func (h *RunsHandler) createManifest(c echo.Context) error {
	if h.cfg != nil && !h.cfg.Server.RunManifestEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "run manifest disabled")
	}
	req := c.Request()
	ctx := req.Context()
	topicID := c.Param("topic_id")
	runID := c.Param("run_id")
	ctx, span := runsTracer.Start(ctx, "RunsHandler.createManifest")
	defer span.End()
	span.SetAttributes(attribute.String("topic_id", topicID), attribute.String("run_id", runID))
	c.SetRequest(req.WithContext(ctx))
	userID := c.Get("user_id").(string)
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	if rec, ok, err := h.store.GetRunManifest(ctx, runID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	} else if ok {
		if rec.TopicID != topicID {
			span.SetStatus(codes.Error, "manifest does not belong to topic")
			return echo.NewHTTPError(http.StatusForbidden, "manifest does not belong to topic")
		}
		var signed manifest.SignedRunManifest
		if err := json.Unmarshal(rec.Manifest, &signed); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		c.Response().Header().Set("X-Run-Manifest-Checksum", rec.Checksum)
		c.Response().Header().Set("X-Run-Manifest-Signature", rec.Signature)
		span.SetAttributes(attribute.String("result", "conflict"))
		return c.JSON(http.StatusConflict, signed)
	}

	ep, ok, err := h.store.GetEpisodeByRunID(ctx, runID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		span.SetStatus(codes.Error, "episode not found")
		return echo.NewHTTPError(http.StatusNotFound, "episode not found")
	}
	if ep.TopicID != topicID {
		span.SetStatus(codes.Error, "episode does not belong to topic")
		return echo.NewHTTPError(http.StatusForbidden, "episode does not belong to topic")
	}
	payload, err := manifest.BuildRunManifest(ep)
	if err != nil {
		h.logger.Printf("manifest build failed: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, "unable to build run manifest")
	}
	secret := h.cfg.Capability.SigningSecret
	if secret == "" {
		span.SetStatus(codes.Error, "signing secret not configured")
		return echo.NewHTTPError(http.StatusInternalServerError, "capability.signing_secret not configured")
	}
	signed, err := manifest.SignRunManifest(payload, secret, time.Now().UTC())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	rec := store.RunManifestRecord{
		RunID:     runID,
		TopicID:   topicID,
		Manifest:  raw,
		Checksum:  signed.Checksum,
		Signature: signed.Signature,
		Algorithm: signed.Algorithm,
		SignedAt:  signed.SignedAt,
	}
	if err := h.store.InsertRunManifest(ctx, rec); err != nil {
		if errors.Is(err, store.ErrRunManifestExists) {
			existing, ok, fetchErr := h.store.GetRunManifest(ctx, runID)
			if fetchErr != nil {
				span.RecordError(fetchErr)
				span.SetStatus(codes.Error, fetchErr.Error())
				return echo.NewHTTPError(http.StatusInternalServerError, fetchErr.Error())
			}
			if ok {
				var current manifest.SignedRunManifest
				if err := json.Unmarshal(existing.Manifest, &current); err == nil {
					c.Response().Header().Set("X-Run-Manifest-Checksum", existing.Checksum)
					c.Response().Header().Set("X-Run-Manifest-Signature", existing.Signature)
					span.SetAttributes(attribute.String("result", "conflict"))
					return c.JSON(http.StatusConflict, current)
				}
			}
			span.SetStatus(codes.Error, "run manifest already exists")
			return echo.NewHTTPError(http.StatusConflict, "run manifest already exists")
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := manifest.VerifyRunManifest(signed, secret); err != nil {
		h.logger.Printf("manifest verify failed after insert: %v", err)
		span.AddEvent("manifest verification warning", trace.WithAttributes(attribute.String("error", err.Error())))
	}
	c.Response().Header().Set("X-Run-Manifest-Checksum", signed.Checksum)
	c.Response().Header().Set("X-Run-Manifest-Signature", signed.Signature)
	span.SetStatus(codes.Ok, "created")
	return c.JSON(http.StatusCreated, signed)
}

// fetchManifest returns the persisted signed manifest for a run.
func (h *RunsHandler) fetchManifest(c echo.Context) error {
	if h.cfg != nil && !h.cfg.Server.RunManifestEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "run manifest disabled")
	}
	req := c.Request()
	ctx := req.Context()
	topicID := c.Param("topic_id")
	runID := c.Param("run_id")
	ctx, span := runsTracer.Start(ctx, "RunsHandler.fetchManifest")
	defer span.End()
	span.SetAttributes(attribute.String("topic_id", topicID), attribute.String("run_id", runID))
	c.SetRequest(req.WithContext(ctx))
	userID := c.Get("user_id").(string)
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	rec, ok, err := h.store.GetRunManifest(ctx, runID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		span.SetStatus(codes.Error, "manifest not found")
		return echo.NewHTTPError(http.StatusNotFound, "manifest not found")
	}
	if rec.TopicID != topicID {
		span.SetStatus(codes.Error, "manifest does not belong to topic")
		return echo.NewHTTPError(http.StatusForbidden, "manifest does not belong to topic")
	}
	var signed manifest.SignedRunManifest
	if err := json.Unmarshal(rec.Manifest, &signed); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	secret := h.cfg.Capability.SigningSecret
	if secret != "" {
		if err := manifest.VerifyRunManifest(signed, secret); err != nil {
			h.logger.Printf("manifest verification warning for run %s: %v", runID, err)
			span.AddEvent("manifest verification warning", trace.WithAttributes(attribute.String("error", err.Error())))
		}
	}
	c.Response().Header().Set("X-Run-Manifest-Checksum", rec.Checksum)
	c.Response().Header().Set("X-Run-Manifest-Signature", rec.Signature)
	span.SetStatus(codes.Ok, "fetched")
	return c.JSON(http.StatusOK, signed)
}

func loadPlanDocument(ctx context.Context, st *store.Store, thoughtID string) (*planner.PlanDocument, error) {
	if thoughtID == "" {
		return nil, fmt.Errorf("thought_id required")
	}
	rec, ok, err := st.GetLatestPlanGraph(ctx, thoughtID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var doc planner.PlanDocument
	if err := json.Unmarshal(rec.PlanJSON, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func attachSemanticContext(ctx context.Context, cfg *config.Config, st *store.Store, provider core.LLMProvider, topicID string, content string, ctxMap map[string]interface{}) {
	if cfg == nil || !cfg.Memory.Semantic.Enabled {
		return
	}
	if provider == nil {
		return
	}
	query := strings.TrimSpace(content)
	if query == "" {
		return
	}
	vectors, err := provider.Embed(ctx, cfg.Memory.Semantic.EmbeddingModel, []string{query})
	if err != nil {
		log.Printf("warn: semantic context embedding failed: %v", err)
		return
	}
	if len(vectors) == 0 {
		return
	}
	topK := cfg.Memory.Semantic.SearchTopK
	if topK <= 0 {
		topK = 5
	}
	threshold := cfg.Memory.Semantic.SearchThreshold
	results, err := st.SearchRunEmbeddings(ctx, topicID, vectors[0], topK, threshold)
	if err != nil {
		log.Printf("warn: semantic context search failed: %v", err)
		return
	}
	if len(results) == 0 {
		return
	}
	entries := make([]map[string]interface{}, 0, len(results))
	for _, hit := range results {
		entry := map[string]interface{}{
			"run_id":     hit.RunID,
			"topic_id":   hit.TopicID,
			"kind":       hit.Kind,
			"distance":   hit.Distance,
			"similarity": similarityFromDistance(hit.Distance),
			"created_at": hit.CreatedAt,
		}
		if hit.Metadata != nil {
			entry["metadata"] = hit.Metadata
		}
		entries = append(entries, entry)
	}
	ctxMap["semantic_similar_runs"] = entries
}

func similarityFromDistance(distance float64) float64 {
	sim := 1 - distance
	if sim < 0 {
		sim = 0
	}
	if sim > 1 {
		sim = 1
	}
	return sim
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
		if err := processRun(ctx, h.cfg, h.store, h.orch, h.embed, h.sem, topicID, userID, runID); err != nil {
			var approval budget.ErrApprovalRequired
			if errors.As(err, &approval) {
				// run remains pending approval; do not mark failed yet
				return
			}
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
	if t, ok := res["created_at"].(string); ok && t != "" {
		b.WriteString("<div class=\"text-xs text-slate-400\">Generated at " + templateEscape(t) + "</div>")
	}
	b.WriteString("</header>")

	// Executive summary
	summary := templateEscape(safeString(res["summary"]))
	if summary != "" {
		b.WriteString("<section><h2 class=\"text-sm font-semibold mb-2\">Executive Summary</h2><p class=\"text-sm\">" + summary + "</p></section>")
	}

	// Items grouped by category with collapsible details
	items, _ := res["metadata"].(map[string]interface{})
	var arr []interface{}
	if items != nil {
		if it, ok := items["items"].([]interface{}); ok {
			arr = it
		}
	}
	if len(arr) > 0 {
		// bucketize
		buckets := map[string][]map[string]interface{}{}
		for _, it := range arr {
			if m, ok := it.(map[string]interface{}); ok {
				c := normCategory(strings.ToLower(safeString(m["category"])))
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
			if len(list) == 0 {
				continue
			}
			b.WriteString("<section class=\"space-y-2\"><h3 class=\"text-sm font-semibold\">" + templateEscape(strings.Title(cat)) + "</h3>")
			for _, m := range list {
				if printed >= maxItems {
					stopped = true
					break
				}
				title := templateEscape(safeString(m["title"]))
				if title == "" {
					title = "Untitled"
				}
				sum := templateEscape(safeString(m["summary"]))
				conf, _ := m["confidence"].(float64)
				low := conf > 0 && conf < 0.6
				badge := ""
				if low {
					badge = "<span class=\"text-amber-400\">⚠️</span> "
				}
				b.WriteString("<details class=\"rounded border border-slate-800 bg-slate-900/40\"><summary class=\"px-3 py-2 cursor-pointer\">" + badge + title + "</summary>")
				b.WriteString("<div class=\"px-3 pb-3 text-sm space-y-2\">")
				if sum != "" {
					b.WriteString("<p>" + sum + "</p>")
				}
				// meta line
				meta := []string{}
				if t := safeString(m["published_at"]); t != "" {
					if tt, err := time.Parse(time.RFC3339, t); err == nil {
						meta = append(meta, templateEscape("🕒 "+tt.Format(time.RFC3339)+" ("+rel(tt)+")"))
					} else {
						meta = append(meta, templateEscape("🕒 "+t))
					}
				}
				if s := safeString(m["seen_at"]); s != "" {
					meta = append(meta, "👁️ "+templateEscape(s))
				}
				if conf > 0 {
					meta = append(meta, fmt.Sprintf("Confidence: %.2f", conf))
				}
				if imp, ok := m["importance"].(float64); ok && imp > 0 {
					meta = append(meta, fmt.Sprintf("Importance: %.2f", imp))
				}
				if len(meta) > 0 {
					b.WriteString("<div class=\"text-xs text-slate-400\">" + templateEscape(strings.Join(meta, " · ")) + "</div>")
				}
				// sources
				if srcs, ok := m["sources"].([]interface{}); ok && len(srcs) > 0 {
					b.WriteString("<div class=\"mt-2\"><div class=\"text-xs text-slate-400\">Sources</div><ul class=\"text-xs list-disc pl-5\">")
					for _, s := range srcs {
						if sm, ok := s.(map[string]interface{}); ok {
							url := templateEscape(safeString(sm["url"]))
							dom := templateEscape(safeString(sm["domain"]))
							arch := templateEscape(safeString(sm["archived_url"]))
							if url != "" {
								label := dom
								if label == "" {
									label = url
								}
								if strings.Contains(strings.ToLower(label), "wikipedia.org") {
									label += " (background)"
								}
								b.WriteString("<li><a class=\"text-sky-400 hover:underline\" href=\"" + url + "\" target=\"_blank\" rel=\"noopener\">" + label + "</a>")
								if arch != "" {
									b.WriteString(" <span class=\"text-slate-500\">(archived: <a class=\"hover:underline\" href=\"" + arch + "\" target=\"_blank\" rel=\"noopener\">" + templateEscape(shortDomain(arch)) + "</a>)</span>")
								}
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
			if stopped {
				break
			}
		}
		if stopped && totalCount > printed {
			b.WriteString("<div class=\"text-xs text-slate-400\">More items available: showing " + fmt.Sprintf("%d of %d", printed, totalCount) + ".</div>")
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

func (h *RunsHandler) budgetDecision(c echo.Context) error {
	ctx := c.Request().Context()
	userID := c.Get("user_id").(string)
	topicID := c.Param("topic_id")
	runID := c.Param("run_id")
	var req struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if _, _, _, err := h.store.GetTopicByID(ctx, topicID, userID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	approval, ok, err := h.store.GetPendingBudgetApproval(ctx, topicID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok || approval.RunID != runID {
		return echo.NewHTTPError(http.StatusBadRequest, "no pending approval for run")
	}
	var reasonPtr *string
	if strings.TrimSpace(req.Reason) != "" {
		reason := strings.TrimSpace(req.Reason)
		reasonPtr = &reason
	}
	if err := h.store.ResolveBudgetApproval(ctx, runID, req.Approved, userID, reasonPtr); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if req.Approved {
		if err := h.store.SetRunStatus(ctx, runID, "running"); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if h.orch != nil {
			go func() {
				workerCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				defer cancel()
				if err := processRun(workerCtx, h.cfg, h.store, h.orch, h.embed, h.sem, topicID, userID, runID); err != nil {
					_ = h.store.FinishRun(workerCtx, runID, "failed", strPtr(err.Error()))
				}
			}()
		}
		return c.JSON(http.StatusAccepted, map[string]string{"status": "approved"})
	}
	// rejected
	if err := h.store.SetRunStatus(ctx, runID, "rejected"); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := h.store.MarkRunBudgetOverrun(ctx, runID, fmt.Sprintf("rejected: %s", strings.TrimSpace(req.Reason))); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := h.store.FinishRun(ctx, runID, "rejected", reasonPtr); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "rejected"})
}

// HTML view for a run's report (feature-flagged optional view). Returns text/html.
//
//	@Summary  Run report as HTML
//	@Tags     runs
//	@Security BearerAuth
//	@Security CookieAuth
//	@Param    topic_id path string true "Topic ID"
//	@Param    run_id   path string true "Run ID"
//	@Produce  html
//	@Success  200 string string
//	@Failure  404 {object} HTTPError
//	@Router   /api/topics/{topic_id}/runs/{run_id}/html [get]
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
	if html == "" {
		return echo.NewHTTPError(http.StatusNotFound, "no report available")
	}
	return c.HTML(http.StatusOK, html)
}

// processRun executes the full run pipeline shared by HTTP trigger and scheduler
func processRun(ctx context.Context, cfg *config.Config, st *store.Store, orch *core.Orchestrator, embed core.LLMProvider, sem *semantic.Ingestor, topicID string, userID string, runID string) error {
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
	ctxMap["topic_id"] = topicID
	ctxMap["user_id"] = userID
	ctxMap["run_id"] = runID

	// Load temporal update policy for the topic (defaults when not configured).
	pol := policy.NewDefault()
	if stored, ok, err := st.GetUpdatePolicy(ctx, topicID); err != nil {
		return fmt.Errorf("load update policy: %w", err)
	} else if ok {
		pol = stored
	}
	policyContext := map[string]interface{}{
		"repeat_mode": string(pol.RepeatMode),
	}
	if pol.RefreshInterval > 0 {
		policyContext["refresh_interval"] = pol.RefreshInterval.String()
	}
	if pol.DedupWindow > 0 {
		policyContext["dedup_window"] = pol.DedupWindow.String()
	}
	if pol.FreshnessThreshold > 0 {
		policyContext["freshness_threshold"] = pol.FreshnessThreshold.String()
	}
	if len(pol.Metadata) > 0 {
		policyContext["metadata"] = pol.Metadata
	}
	ctxMap["temporal_policy"] = policyContext

	// Fetch topic (name, preferences)
	name, prefsB, _, err := st.GetTopicByID(ctx, topicID, userID)
	if err != nil {
		return err
	}
	var prefs Preferences
	_ = json.Unmarshal(prefsB, &prefs)

	// Construct thought from preferences (not user-defined name)
	content := deriveThoughtContentFromPrefs(prefs)
	attachSemanticContext(ctx, cfg, st, embed, topicID, content, ctxMap)
	budgetCfg, hasBudget, err := st.GetTopicBudgetConfig(ctx, topicID)
	if err != nil {
		return fmt.Errorf("load budget config: %w", err)
	}
	if hasBudget && !budgetCfg.IsZero() {
		budgetContext := map[string]interface{}{}
		if budgetCfg.MaxCost != nil {
			budgetContext["max_cost"] = *budgetCfg.MaxCost
		}
		if budgetCfg.MaxTokens != nil {
			budgetContext["max_tokens"] = *budgetCfg.MaxTokens
		}
		if budgetCfg.MaxTimeSeconds != nil {
			budgetContext["max_time_seconds"] = *budgetCfg.MaxTimeSeconds
		}
		if budgetCfg.ApprovalThreshold != nil {
			budgetContext["approval_threshold"] = *budgetCfg.ApprovalThreshold
		}
		budgetContext["require_approval"] = budgetCfg.RequireApproval
		if len(budgetCfg.Metadata) > 0 {
			budgetContext["metadata"] = budgetCfg.Metadata
		}
		ctxMap["budget"] = budgetContext
	}
	var thoughtBudget *budget.Config
	if hasBudget && !budgetCfg.IsZero() {
		cfgClone := budgetCfg.Clone()
		thoughtBudget = &cfgClone
		if err := st.ApplyRunBudget(ctx, runID, cfgClone); err != nil {
			return fmt.Errorf("apply run budget: %w", err)
		}
	}
	policyCopy := pol.Clone()
	thought := core.UserThought{ID: runID, UserID: userID, TopicID: topicID, Content: content, Preferences: prefs, Timestamp: time.Now(), Context: ctxMap, Policy: &policyCopy, Budget: thoughtBudget}

	result, err := orch.ProcessThought(ctx, thought)
	if err != nil {
		var exceeded budget.ErrExceeded
		var approval budget.ErrApprovalRequired
		switch {
		case errors.As(err, &approval):
			if hasBudget {
				_ = st.MarkRunPendingApproval(ctx, runID)
				_ = st.CreateBudgetApproval(ctx, runID, topicID, userID, approval.EstimatedCost, approval.Threshold)
			}
		case errors.As(err, &exceeded):
			_ = st.MarkRunBudgetOverrun(ctx, runID, exceeded.Error())
		}
		return err
	}

	// Persist
	_ = st.UpsertProcessingResult(ctx, result)
	if len(result.Highlights) > 0 {
		_ = st.SaveHighlights(ctx, topicID, result.Highlights)
	}
	_ = st.SaveKnowledgeGraphFromMetadata(ctx, topicID, result.Metadata)
	if sem != nil {
		planDoc, err := loadPlanDocument(ctx, st, runID)
		if err != nil {
			log.Printf("warn: load plan for semantic ingest failed: %v", err)
		} else if err := sem.IngestRun(ctx, topicID, runID, result, planDoc); err != nil {
			log.Printf("warn: semantic ingest failed: %v", err)
		}
	}
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
				if c, ok := stats["count"].(int); ok {
					fmt.Fprintf(&b, "- Items: %d\n", c)
				}
				if c, ok := asIntIface(stats["count"]); ok {
					fmt.Fprintf(&b, "- Items: %d\n", c)
				}
				if cats, ok := stats["categories"].(map[string]interface{}); ok {
					var lines []string
					for k, v := range cats {
						if n, ok := asIntIface(v); ok {
							lines = append(lines, fmt.Sprintf("%s: %d", k, n))
						}
					}
					sort.Strings(lines)
					if len(lines) > 0 {
						fmt.Fprintf(&b, "- Categories: %s\n", strings.Join(lines, ", "))
					}
				}
				if span, ok := stats["span_hours"].(float64); ok {
					fmt.Fprintf(&b, "- Span: %.0fh\n", span)
				}
				if top, ok := stats["top_domains"].([]interface{}); ok {
					var ds []string
					for _, d := range top {
						if s, ok := d.(string); ok {
							ds = append(ds, s)
						}
					}
					if len(ds) > 0 {
						fmt.Fprintf(&b, "- Top sources: %s\n", strings.Join(ds, ", "))
					}
				}
				b.WriteString("\n")
			}
			// Table of contents
			fmt.Fprintf(&b, "## Contents\n\n")
			for i, it := range items {
				if m, ok := it.(map[string]interface{}); ok {
					title := safeString(m["title"])
					if title == "" {
						title = fmt.Sprintf("Item %d", i+1)
					}
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
				if len(arr) == 0 {
					continue
				}
				// category header
				fmt.Fprintf(&b, "### %s %s\n\n", badgeFor(cat), strings.Title(cat))
				for _, m := range arr {
					if printed >= maxItems {
						stopped = true
						break
					}
					title := safeString(m["title"])
					if title == "" {
						title = "Untitled"
					}
					anchor := slugify(title)
					// confidence badge if low
					lowConf := false
					if conf, ok := m["confidence"].(float64); ok && conf > 0 && conf < 0.6 {
						lowConf = true
					}
					header := title
					if lowConf {
						header = "⚠️ " + header
					}
					// item header
					fmt.Fprintf(&b, "#### %s\n\n", header)
					fmt.Fprintf(&b, "<a id=\"%s\"></a>\n\n", anchor)
					sum := safeString(m["summary"])
					if sum != "" {
						fmt.Fprintf(&b, "%s\n\n", sum)
					}
					meta := []string{}
					if t := safeString(m["published_at"]); t != "" {
						if tt, err := time.Parse(time.RFC3339, t); err == nil {
							meta = append(meta, fmt.Sprintf("🕒 %s (%s)", tt.Format(time.RFC3339), rel(tt)))
						} else {
							meta = append(meta, fmt.Sprintf("🕒 %s", t))
						}
					}
					if s := safeString(m["seen_at"]); s != "" {
						meta = append(meta, fmt.Sprintf("👁️ %s", s))
					}
					if conf, ok := m["confidence"].(float64); ok && conf > 0 {
						meta = append(meta, fmt.Sprintf("Confidence: %.2f", conf))
					}
					if imp, ok := m["importance"].(float64); ok && imp > 0 {
						meta = append(meta, fmt.Sprintf("Importance: %.2f", imp))
					}
					if len(meta) > 0 {
						fmt.Fprintf(&b, "_Meta_: %s\n\n", strings.Join(meta, " · "))
					}
					if lowConf {
						b.WriteString("> Note: This item has low confidence; consider verifying with additional sources.\n\n")
					}
					if srcs, ok := m["sources"].([]interface{}); ok && len(srcs) > 0 {
						// Reorder to put non-wikipedia first
						var list []map[string]interface{}
						for _, s := range srcs {
							if sm, ok := s.(map[string]interface{}); ok {
								list = append(list, sm)
							}
						}
						sort.SliceStable(list, func(i, j int) bool {
							di := strings.ToLower(safeString(list[i]["domain"]))
							dj := strings.ToLower(safeString(list[j]["domain"]))
							wi := strings.Contains(di, "wikipedia.org")
							wj := strings.Contains(dj, "wikipedia.org")
							if wi == wj {
								return di < dj
							}
							return !wi && wj
						})
						b.WriteString("Sources:\n\n")
						for _, sm := range list {
							url := safeString(sm["url"])
							dom := safeString(sm["domain"])
							arch := safeString(sm["archived_url"])
							label := dom
							if label == "" {
								label = url
							}
							if strings.Contains(strings.ToLower(dom), "wikipedia.org") {
								label += " (background)"
							}
							if url != "" {
								fmt.Fprintf(&b, "- [%s](%s)", label, url)
								if arch != "" {
									fmt.Fprintf(&b, " (archived: [%s](%s))", shortDomain(arch), arch)
								}
								b.WriteString("\n")
							}
						}
						b.WriteString("\n")
					}
					printed++
				}
				b.WriteString("\n")
				if stopped {
					break
				}
			}
			if stopped && totalCount > printed {
				fmt.Fprintf(&b, "_More items available: showing %d of %d._\n\n", printed, totalCount)
			}
			// Source appendix
			fmt.Fprintf(&b, "## Source List (Appendix)\n\n")
			uniq := map[string]bool{}
			for _, it := range items {
				if m, ok := it.(map[string]interface{}); ok {
					pub := safeString(m["published_at"])
					if pub == "" {
						pub = "N/A"
					}
					if srcs, ok := m["sources"].([]interface{}); ok {
						for _, s := range srcs {
							if sm, ok := s.(map[string]interface{}); ok {
								url := safeString(sm["url"])
								if url == "" {
									continue
								}
								if uniq[url] {
									continue
								}
								uniq[url] = true
								dom := safeString(sm["domain"])
								if dom == "" {
									dom = shortDomain(url)
								}
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
		if cost > 0 {
			fmt.Fprintf(&b, "; Cost: $%.4f", cost)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func safeString(v interface{}) string {
	if v == nil {
		return ""
	}
        if s, ok := v.(string); ok {
                return helpers.SanitizeHTMLStrict(s)
        }
        b, _ := json.Marshal(v)
        return helpers.SanitizeHTMLStrict(string(b))
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

func extractCostEstimate(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return f, true
		}
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint64:
		return float64(val), true
	case string:
		if num, err := strconv.ParseFloat(val, 64); err == nil {
			return num, true
		}
	case json.RawMessage:
		var num json.Number
		if err := json.Unmarshal(val, &num); err == nil {
			if f, err := num.Float64(); err == nil {
				return f, true
			}
		}
	}
	return 0, false
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
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours()) / 24
	if days < 30 {
		return fmt.Sprintf("%dd ago", days)
	}
	months := days / 30
	if months < 12 {
		return fmt.Sprintf("%dmo ago", months)
	}
	years := months / 12
	return fmt.Sprintf("%dy ago", years)
}
