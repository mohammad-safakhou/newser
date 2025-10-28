package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/memory/templates"
	"github.com/mohammad-safakhou/newser/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type storeAPI interface {
	SaveEpisode(ctx context.Context, ep store.Episode) error
	ListRuns(ctx context.Context, topicID string) ([]store.Run, error)
	GetEpisodeByRunID(ctx context.Context, runID string) (store.Episode, bool, error)
	HasSimilarRunEmbedding(ctx context.Context, topicID string, vector []float32, threshold float64, window time.Duration) (bool, error)
	HasSimilarPlanStepEmbedding(ctx context.Context, topicID string, vector []float32, threshold float64, window time.Duration) (bool, error)
	LogMemoryDelta(ctx context.Context, rec store.MemoryDeltaRecord) error
	CreateMemoryJob(ctx context.Context, rec store.MemoryJobRecord) (store.MemoryJobRecord, error)
	CompleteMemoryJob(ctx context.Context, jobID int64, status string, result store.MemoryJobResultRecord) error
	PruneRunEmbeddingsBefore(ctx context.Context, cutoff time.Time) (int64, error)
	PrunePlanStepEmbeddingsBefore(ctx context.Context, cutoff time.Time) (int64, error)
	MemoryHealthStats(ctx context.Context, window time.Duration) (store.MemoryHealthStats, error)
	ListProceduralTemplateFingerprints(ctx context.Context, topicID string, limit int) ([]store.ProceduralTemplateFingerprintRecord, error)
	CreateProceduralTemplate(ctx context.Context, rec store.ProceduralTemplateRecord) (store.ProceduralTemplateRecord, error)
	CreateProceduralTemplateVersion(ctx context.Context, rec store.ProceduralTemplateVersionRecord) (store.ProceduralTemplateVersionRecord, error)
	LinkProceduralTemplateFingerprint(ctx context.Context, topicID, fingerprint, templateID string) error
	ListProceduralTemplates(ctx context.Context, topicID string) ([]store.ProceduralTemplateRecord, error)
	ListProceduralTemplateVersions(ctx context.Context, templateID string) ([]store.ProceduralTemplateVersionRecord, error)
}

// Manager provides a lightweight episodic memory implementation backed by the primary store.
type Manager struct {
	store           storeAPI
	cfg             config.MemoryConfig
	logger          *log.Logger
	episodicEnabled bool
	semanticEnabled bool
	provider        agentcore.LLMProvider
	deltaThreshold  float64
	deltaWindow     time.Duration
	deltaWithSteps  bool
	deltaTotal      otelmetric.Int64Counter
	deltaNovel      otelmetric.Int64Counter
	deltaDuplicates otelmetric.Int64Counter
	deltaLatency    otelmetric.Float64Histogram
	summaryLatency  otelmetric.Float64Histogram
	summaryTotal    otelmetric.Int64Counter
	summaryFailures otelmetric.Int64Counter
	pruneLatency    otelmetric.Float64Histogram
	pruneTotal      otelmetric.Int64Counter
	pruneFailures   otelmetric.Int64Counter
	pruneRuns       otelmetric.Int64Counter
	pruneSteps      otelmetric.Int64Counter
	jobStatus       otelmetric.Int64Counter
	healthEpisodes  otelmetric.Int64Histogram
	healthRuns      otelmetric.Int64Histogram
	healthSteps     otelmetric.Int64Histogram
	healthNovel     otelmetric.Int64Histogram
	healthDuplicate otelmetric.Int64Histogram
	templates       *templates.Manager
}

// SemanticPruneStats captures counts of semantic embeddings removed during pruning.
type SemanticPruneStats struct {
	RunEmbeddings  int64
	PlanEmbeddings int64
}

// Total returns the combined embeddings removed across run and plan-step stores.
func (s SemanticPruneStats) Total() int64 {
	return s.RunEmbeddings + s.PlanEmbeddings
}

// New constructs a memory manager for episodic and semantic memory operations.
func New(st storeAPI, cfg config.MemoryConfig, provider agentcore.LLMProvider, logger *log.Logger) *Manager {
	if st == nil {
		return nil
	}
	episodicEnabled := cfg.Episodic.Enabled
	semanticCfg := cfg.Semantic
	semanticEnabled := semanticCfg.Enabled && provider != nil
	if !episodicEnabled && !semanticEnabled {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[MEMORY] ", log.LstdFlags)
	}
	threshold := semanticCfg.DeltaThreshold
	if threshold <= 0 {
		threshold = semanticCfg.SearchThreshold
	}
	if threshold <= 0 {
		threshold = 0.8
	}
	window := semanticCfg.DeltaWindow
	if window < 0 {
		window = 0
	}
	mgr := &Manager{
		store:           st,
		cfg:             cfg,
		logger:          logger,
		episodicEnabled: episodicEnabled,
		semanticEnabled: semanticEnabled,
		provider:        provider,
		deltaThreshold:  threshold,
		deltaWindow:     window,
		deltaWithSteps:  semanticCfg.DeltaIncludeSteps,
	}
	meter := otel.Meter("memory/manager")
	var err error
	mgr.deltaTotal, err = meter.Int64Counter("memory_delta_items")
	if err != nil {
		logger.Printf("otel counter memory_delta_items: %v", err)
	}
	mgr.deltaNovel, err = meter.Int64Counter("memory_delta_novel")
	if err != nil {
		logger.Printf("otel counter memory_delta_novel: %v", err)
	}
	mgr.deltaDuplicates, err = meter.Int64Counter("memory_delta_duplicates")
	if err != nil {
		logger.Printf("otel counter memory_delta_duplicates: %v", err)
	}
	mgr.deltaLatency, err = meter.Float64Histogram("memory_delta_latency_ms", otelmetric.WithUnit("ms"))
	if err != nil {
		logger.Printf("otel histogram memory_delta_latency_ms: %v", err)
	}
	mgr.summaryLatency, err = meter.Float64Histogram("memory_summary_latency_ms", otelmetric.WithUnit("ms"))
	if err != nil {
		logger.Printf("otel histogram memory_summary_latency_ms: %v", err)
	}
	mgr.summaryTotal, err = meter.Int64Counter("memory_summary_runs")
	if err != nil {
		logger.Printf("otel counter memory_summary_runs: %v", err)
	}
	mgr.summaryFailures, err = meter.Int64Counter("memory_summary_failures")
	if err != nil {
		logger.Printf("otel counter memory_summary_failures: %v", err)
	}
	mgr.pruneLatency, err = meter.Float64Histogram("memory_prune_latency_ms", otelmetric.WithUnit("ms"))
	if err != nil {
		logger.Printf("otel histogram memory_prune_latency_ms: %v", err)
	}
	mgr.pruneTotal, err = meter.Int64Counter("memory_prune_runs")
	if err != nil {
		logger.Printf("otel counter memory_prune_runs: %v", err)
	}
	mgr.pruneFailures, err = meter.Int64Counter("memory_prune_failures")
	if err != nil {
		logger.Printf("otel counter memory_prune_failures: %v", err)
	}
	mgr.pruneRuns, err = meter.Int64Counter("memory_pruned_run_embeddings")
	if err != nil {
		logger.Printf("otel counter memory_pruned_run_embeddings: %v", err)
	}
	mgr.pruneSteps, err = meter.Int64Counter("memory_pruned_plan_embeddings")
	if err != nil {
		logger.Printf("otel counter memory_pruned_plan_embeddings: %v", err)
	}
	mgr.jobStatus, err = meter.Int64Counter("memory_job_status_total")
	if err != nil {
		logger.Printf("otel counter memory_job_status_total: %v", err)
	}
	mgr.healthEpisodes, err = meter.Int64Histogram("memory_episodes_sample")
	if err != nil {
		logger.Printf("otel histogram memory_episodes_sample: %v", err)
	}
	mgr.healthRuns, err = meter.Int64Histogram("memory_run_embeddings_sample")
	if err != nil {
		logger.Printf("otel histogram memory_run_embeddings_sample: %v", err)
	}
	mgr.healthSteps, err = meter.Int64Histogram("memory_plan_embeddings_sample")
	if err != nil {
		logger.Printf("otel histogram memory_plan_embeddings_sample: %v", err)
	}
	mgr.healthNovel, err = meter.Int64Histogram("memory_delta_novel_sample")
	if err != nil {
		logger.Printf("otel histogram memory_delta_novel_sample: %v", err)
	}
	mgr.healthDuplicate, err = meter.Int64Histogram("memory_delta_duplicate_sample")
	if err != nil {
		logger.Printf("otel histogram memory_delta_duplicate_sample: %v", err)
	}
	mgr.templates = templates.NewManager(st, logger)
	return mgr
}

// WriteEpisode persists an episodic snapshot for later replay and summarisation.
func (m *Manager) WriteEpisode(ctx context.Context, snapshot agentcore.EpisodicSnapshot) error {
	if m == nil {
		return fmt.Errorf("memory manager unavailable")
	}
	if !m.episodicEnabled {
		return fmt.Errorf("episodic memory disabled")
	}
	ep, err := snapshotToEpisode(snapshot)
	if err != nil {
		return err
	}
	if err := m.store.SaveEpisode(ctx, ep); err != nil {
		return fmt.Errorf("save episode: %w", err)
	}
	return nil
}

// Summarize aggregates recent run summaries for a topic.
func (m *Manager) Summarize(ctx context.Context, req memorysvc.SummaryRequest) (resp memorysvc.SummaryResponse, err error) {
	if m == nil {
		err = fmt.Errorf("memory manager unavailable")
		return
	}
	if !m.episodicEnabled {
		err = fmt.Errorf("episodic memory disabled")
		return
	}
	if strings.TrimSpace(req.TopicID) == "" {
		err = fmt.Errorf("topic_id required")
		return
	}

	maxRuns := req.MaxRuns
	if maxRuns <= 0 {
		maxRuns = 5
	}
	if maxRuns > 20 {
		maxRuns = 20
	}

	var job *store.MemoryJobRecord
	start := time.Now()
	if m.store != nil {
		params, marshalErr := json.Marshal(map[string]interface{}{
			"max_runs": maxRuns,
		})
		if marshalErr != nil {
			params = []byte(`{"max_runs":` + fmt.Sprintf("%d", maxRuns) + `}`)
		}
		started := time.Now().UTC()
		if created, createErr := m.store.CreateMemoryJob(ctx, store.MemoryJobRecord{
			TopicID:      req.TopicID,
			JobType:      store.MemoryJobTypeSummarise,
			Status:       store.MemoryJobStatusRunning,
			ScheduledFor: started,
			StartedAt:    &started,
			Params:       params,
		}); createErr == nil {
			job = &created
		} else if m.logger != nil {
			m.logger.Printf("warn: create memory job failed: %v", createErr)
		}
	}

	defer func() {
		attrs := []attribute.KeyValue{attribute.String("topic_id", req.TopicID)}
		elapsedMs := time.Since(start).Seconds() * 1000
		if m.summaryLatency != nil {
			m.summaryLatency.Record(ctx, elapsedMs, otelmetric.WithAttributes(attrs...))
		}
		if err != nil {
			if m.summaryFailures != nil {
				m.summaryFailures.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
			}
		} else {
			if m.summaryTotal != nil {
				m.summaryTotal.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
			}
		}
		if job == nil {
			return
		}
		status := store.MemoryJobStatusSuccess
		errorMsg := ""
		if err != nil {
			status = store.MemoryJobStatusFailed
			errorMsg = err.Error()
		}
		metricsBytes, metricsErr := json.Marshal(map[string]interface{}{
			"runs_considered": len(resp.Items),
			"max_runs":        maxRuns,
			"summary_length":  len(resp.Summary),
		})
		if metricsErr != nil {
			metricsBytes = []byte("{}")
		}
		summaryText := strings.TrimSpace(resp.Summary)
		if summaryText == "" && status == store.MemoryJobStatusSuccess {
			summaryText = fmt.Sprintf("processed %d runs", len(resp.Items))
		}
		if status == store.MemoryJobStatusFailed {
			summaryText = ""
		}
		if completeErr := m.store.CompleteMemoryJob(ctx, job.ID, status, store.MemoryJobResultRecord{
			JobID:    job.ID,
			Metrics:  metricsBytes,
			Summary:  summaryText,
			ErrorMsg: errorMsg,
		}); completeErr != nil {
			if m.logger != nil {
				m.logger.Printf("warn: complete memory job failed: %v", completeErr)
			}
			status = store.MemoryJobStatusFailed
		}
		if m.jobStatus != nil {
			jobAttrs := []attribute.KeyValue{
				attribute.String("job_type", store.MemoryJobTypeSummarise),
				attribute.String("status", status),
			}
			m.jobStatus.Add(ctx, 1, otelmetric.WithAttributes(jobAttrs...))
		}
	}()

	runs, listErr := m.store.ListRuns(ctx, req.TopicID)
	if listErr != nil {
		err = fmt.Errorf("list runs: %w", listErr)
		return
	}

	resp = memorysvc.SummaryResponse{TopicID: req.TopicID, GeneratedAt: time.Now().UTC()}
	limit := maxRuns
	if len(runs) < limit {
		limit = len(runs)
	}
	var lines []string
	for i := 0; i < limit; i++ {
		run := runs[i]
		episode, ok, getErr := m.store.GetEpisodeByRunID(ctx, run.ID)
		if getErr != nil {
			err = fmt.Errorf("get episode %s: %w", run.ID, getErr)
			return
		}
		if !ok {
			continue
		}
		summary := strings.TrimSpace(episode.Result.Summary)
		if summary == "" {
			summary = deriveFallbackSummary(episode.Result)
		}
		item := memorysvc.SummaryItem{
			RunID:     run.ID,
			Summary:   summary,
			StartedAt: run.StartedAt,
		}
		if run.FinishedAt != nil {
			item.FinishedAt = run.FinishedAt
		}
		resp.Items = append(resp.Items, item)
		if summary != "" {
			lines = append(lines, "- "+summary)
		}
	}
	resp.Summary = strings.Join(lines, "\n")
	return resp, nil
}

// PruneSemanticEmbeddings removes aged semantic vectors to control storage growth.
func (m *Manager) PruneSemanticEmbeddings(ctx context.Context, cutoff time.Time) (stats SemanticPruneStats, err error) {
	if m == nil {
		err = fmt.Errorf("memory manager unavailable")
		return
	}
	if !m.semanticEnabled {
		err = fmt.Errorf("semantic memory disabled")
		return
	}
	if cutoff.IsZero() {
		err = fmt.Errorf("cutoff required")
		return
	}
	if m.store == nil {
		err = fmt.Errorf("store unavailable")
		return
	}

	params, marshalErr := json.Marshal(map[string]interface{}{
		"cutoff": cutoff.UTC().Format(time.RFC3339),
	})
	if marshalErr != nil {
		params = []byte("{}")
	}
	var job *store.MemoryJobRecord
	started := time.Now().UTC()
	if created, createErr := m.store.CreateMemoryJob(ctx, store.MemoryJobRecord{
		JobType:      store.MemoryJobTypePrune,
		Status:       store.MemoryJobStatusRunning,
		ScheduledFor: started,
		StartedAt:    &started,
		Params:       params,
	}); createErr == nil {
		job = &created
	} else if m.logger != nil {
		m.logger.Printf("warn: create semantic prune job failed: %v", createErr)
	}

	start := time.Now()
	defer func() {
		attrs := []attribute.KeyValue{attribute.Bool("semantic", true)}
		if m.pruneLatency != nil {
			m.pruneLatency.Record(ctx, time.Since(start).Seconds()*1000, otelmetric.WithAttributes(attrs...))
		}
		if err != nil {
			if m.pruneFailures != nil {
				m.pruneFailures.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
			}
		} else {
			if m.pruneTotal != nil {
				m.pruneTotal.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
			}
			if m.pruneRuns != nil && stats.RunEmbeddings > 0 {
				m.pruneRuns.Add(ctx, stats.RunEmbeddings, otelmetric.WithAttributes(attrs...))
			}
			if m.pruneSteps != nil && stats.PlanEmbeddings > 0 {
				m.pruneSteps.Add(ctx, stats.PlanEmbeddings, otelmetric.WithAttributes(attrs...))
			}
		}
		if job == nil {
			return
		}
		status := store.MemoryJobStatusSuccess
		errorMsg := ""
		if err != nil {
			status = store.MemoryJobStatusFailed
			errorMsg = err.Error()
		}
		jobMetrics, metricsErr := json.Marshal(map[string]interface{}{
			"run_embeddings_pruned":  stats.RunEmbeddings,
			"plan_embeddings_pruned": stats.PlanEmbeddings,
			"cutoff":                 cutoff.UTC().Format(time.RFC3339),
		})
		if metricsErr != nil {
			jobMetrics = []byte("{}")
		}
		summary := fmt.Sprintf("pruned %d run embeddings, %d plan embeddings", stats.RunEmbeddings, stats.PlanEmbeddings)
		if status == store.MemoryJobStatusFailed {
			summary = ""
		}
		if completeErr := m.store.CompleteMemoryJob(ctx, job.ID, status, store.MemoryJobResultRecord{
			JobID:    job.ID,
			Metrics:  jobMetrics,
			Summary:  summary,
			ErrorMsg: errorMsg,
		}); completeErr != nil {
			if m.logger != nil {
				m.logger.Printf("warn: complete semantic prune job failed: %v", completeErr)
			}
		}
		if m.jobStatus != nil {
			m.jobStatus.Add(ctx, 1, otelmetric.WithAttributes(
				attribute.String("job_type", store.MemoryJobTypePrune),
				attribute.String("status", status),
			))
		}
	}()

	runs, pruneErr := m.store.PruneRunEmbeddingsBefore(ctx, cutoff)
	if pruneErr != nil {
		err = fmt.Errorf("prune run embeddings: %w", pruneErr)
		return
	}
	stats.RunEmbeddings = runs
	steps, pruneStepsErr := m.store.PrunePlanStepEmbeddingsBefore(ctx, cutoff)
	if pruneStepsErr != nil {
		err = fmt.Errorf("prune plan embeddings: %w", pruneStepsErr)
		return
	}
	stats.PlanEmbeddings = steps
	return
}

func (m *Manager) ListFingerprints(ctx context.Context, topicID string, limit int) ([]agentcore.TemplateFingerprintState, error) {
	tm, err := m.templateManager()
	if err != nil {
		return nil, err
	}
	return tm.ListFingerprints(ctx, topicID, limit)
}

func (m *Manager) PromoteFingerprint(ctx context.Context, req memorysvc.TemplatePromotionRequest) (agentcore.ProceduralTemplate, error) {
	tm, err := m.templateManager()
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}
	return tm.PromoteFingerprint(ctx, req)
}

func (m *Manager) ListTemplates(ctx context.Context, topicID string) ([]agentcore.ProceduralTemplate, error) {
	tm, err := m.templateManager()
	if err != nil {
		return nil, err
	}
	return tm.ListTemplates(ctx, topicID)
}

func (m *Manager) ApproveTemplate(ctx context.Context, req memorysvc.TemplateApprovalRequest) (agentcore.ProceduralTemplate, error) {
	tm, err := m.templateManager()
	if err != nil {
		return agentcore.ProceduralTemplate{}, err
	}
	return tm.ApproveTemplate(ctx, req)
}

func (m *Manager) Health(ctx context.Context) (memorysvc.HealthStats, error) {
	if m == nil || m.store == nil {
		return memorysvc.HealthStats{}, fmt.Errorf("memory manager unavailable")
	}
	stats, err := m.store.MemoryHealthStats(ctx, 24*time.Hour)
	if err != nil {
		return memorysvc.HealthStats{}, err
	}
	out := memorysvc.HealthStats{
		Episodes:          stats.Episodes,
		RunEmbeddings:     stats.RunEmbeddings,
		PlanEmbeddings:    stats.PlanEmbeddings,
		NovelDeltaCount:   stats.NovelDeltaCount,
		DuplicateDeltaCnt: stats.DuplicateDeltaCnt,
		CollectedAt:       stats.CollectedAt.UTC().Format(time.RFC3339),
	}
	if stats.LastDeltaAt != nil {
		out.LastDeltaAt = stats.LastDeltaAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// Delta filters already-seen items using hashes and semantic similarity when available.
func (m *Manager) Delta(ctx context.Context, req memorysvc.DeltaRequest) (memorysvc.DeltaResponse, error) {
	if m == nil {
		return memorysvc.DeltaResponse{}, fmt.Errorf("memory manager unavailable")
	}
	if strings.TrimSpace(req.TopicID) == "" {
		return memorysvc.DeltaResponse{}, fmt.Errorf("topic_id required")
	}
	known := make(map[string]struct{}, len(req.KnownIDs))
	for _, id := range req.KnownIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		known[id] = struct{}{}
	}
	seen := make(map[string]struct{})
	resp := memorysvc.DeltaResponse{TopicID: req.TopicID, Total: len(req.Items)}
	start := time.Now()
	success := false
	defer func() {
		if !success {
			return
		}
		attrs := []attribute.KeyValue{attribute.Bool("semantic", m.semanticEnabled)}
		if m.deltaTotal != nil {
			m.deltaTotal.Add(ctx, int64(resp.Total), otelmetric.WithAttributes(attrs...))
		}
		if m.deltaNovel != nil {
			m.deltaNovel.Add(ctx, int64(len(resp.Novel)), otelmetric.WithAttributes(attrs...))
		}
		if m.deltaDuplicates != nil {
			m.deltaDuplicates.Add(ctx, int64(resp.DuplicateCount), otelmetric.WithAttributes(attrs...))
		}
		if m.deltaLatency != nil {
			m.deltaLatency.Record(ctx, time.Since(start).Seconds()*1000, otelmetric.WithAttributes(attrs...))
		}
	}()
	if len(req.Items) == 0 {
		m.persistDelta(ctx, resp)
		success = true
		return resp, nil
	}

	var candidates []deltaCandidate
	for _, item := range req.Items {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = strings.TrimSpace(item.Hash)
		}
		if key == "" {
			resp.Novel = append(resp.Novel, item)
			continue
		}
		if _, ok := known[key]; ok {
			resp.DuplicateCount++
			continue
		}
		if _, ok := seen[key]; ok {
			resp.DuplicateCount++
			continue
		}
		seen[key] = struct{}{}
		if !m.semanticEnabled {
			resp.Novel = append(resp.Novel, item)
			continue
		}
		text := deltaText(item)
		if text == "" {
			resp.Novel = append(resp.Novel, item)
			continue
		}
		candidates = append(candidates, deltaCandidate{Item: item, Text: text})
	}

	if !m.semanticEnabled || len(candidates) == 0 {
		m.persistDelta(ctx, resp)
		success = true
		return resp, nil
	}

	texts := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		texts = append(texts, cand.Text)
	}
	vectors, err := m.provider.Embed(ctx, m.cfg.Semantic.EmbeddingModel, texts)
	if err != nil {
		return memorysvc.DeltaResponse{}, fmt.Errorf("embed delta candidates: %w", err)
	}
	if len(vectors) != len(candidates) {
		return memorysvc.DeltaResponse{}, fmt.Errorf("embedding provider returned %d vectors for %d candidates", len(vectors), len(candidates))
	}

	for idx, cand := range candidates {
		vec := vectors[idx]
		if len(vec) == 0 {
			resp.Novel = append(resp.Novel, cand.Item)
			continue
		}
		similar, err := m.store.HasSimilarRunEmbedding(ctx, req.TopicID, vec, m.deltaThreshold, m.deltaWindow)
		if err != nil {
			return memorysvc.DeltaResponse{}, fmt.Errorf("search run embeddings: %w", err)
		}
		if !similar && m.deltaWithSteps {
			similar, err = m.store.HasSimilarPlanStepEmbedding(ctx, req.TopicID, vec, m.deltaThreshold, m.deltaWindow)
			if err != nil {
				return memorysvc.DeltaResponse{}, fmt.Errorf("search plan embeddings: %w", err)
			}
		}
		if similar {
			resp.DuplicateCount++
			continue
		}
		resp.Novel = append(resp.Novel, cand.Item)
	}
	m.persistDelta(ctx, resp)
	success = true
	return resp, nil
}

type deltaCandidate struct {
	Item memorysvc.DeltaItem
	Text string
}

func (m *Manager) persistDelta(ctx context.Context, resp memorysvc.DeltaResponse) {
	if m.store == nil {
		return
	}
	meta := map[string]interface{}{
		"novel_ids": extractDeltaIDs(resp.Novel),
	}
	rec := store.MemoryDeltaRecord{
		TopicID:        resp.TopicID,
		TotalItems:     resp.Total,
		NovelItems:     len(resp.Novel),
		DuplicateItems: resp.DuplicateCount,
		Semantic:       m.semanticEnabled,
		Metadata:       meta,
	}
	if err := m.store.LogMemoryDelta(ctx, rec); err != nil && m.logger != nil {
		m.logger.Printf("warn: log memory delta failed: %v", err)
	}
}

// RecordHealthSnapshot emits telemetry metrics describing overall memory health.
func (m *Manager) RecordHealthSnapshot(ctx context.Context, stats store.MemoryHealthStats) {
	if m == nil {
		return
	}
	attrs := []attribute.KeyValue{}
	if m.healthEpisodes != nil {
		m.healthEpisodes.Record(ctx, stats.Episodes, otelmetric.WithAttributes(attrs...))
	}
	if m.healthRuns != nil {
		m.healthRuns.Record(ctx, stats.RunEmbeddings, otelmetric.WithAttributes(attrs...))
	}
	if m.healthSteps != nil {
		m.healthSteps.Record(ctx, stats.PlanEmbeddings, otelmetric.WithAttributes(attrs...))
	}
	if m.healthNovel != nil {
		m.healthNovel.Record(ctx, stats.NovelDeltaCount, otelmetric.WithAttributes(attrs...))
	}
	if m.healthDuplicate != nil {
		m.healthDuplicate.Record(ctx, stats.DuplicateDeltaCnt, otelmetric.WithAttributes(attrs...))
	}
}

func (m *Manager) templateManager() (*templates.Manager, error) {
	if m == nil || m.templates == nil {
		return nil, fmt.Errorf("template management unavailable")
	}
	return m.templates, nil
}

func deltaText(item memorysvc.DeltaItem) string {
	keys := []string{"text", "summary", "content", "body", "description"}
	for _, key := range keys {
		if val, ok := lookupString(item.Payload, key); ok {
			return val
		}
		if val, ok := lookupString(item.Metadata, key); ok {
			return val
		}
	}
	var parts []string
	for key, raw := range item.Payload {
		if str, ok := raw.(string); ok {
			val := strings.TrimSpace(str)
			if val != "" {
				parts = append(parts, fmt.Sprintf("%s: %s", key, val))
			}
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, "\n")
	if len(joined) > 4096 {
		joined = joined[:4096]
	}
	return joined
}

func lookupString(m map[string]interface{}, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				return trimmed, true
			}
		}
	}
	return "", false
}

func extractDeltaIDs(items []memorysvc.DeltaItem) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
			continue
		}
		if hash := strings.TrimSpace(item.Hash); hash != "" {
			ids = append(ids, hash)
		}
	}
	return ids
}

func deriveFallbackSummary(res agentcore.ProcessingResult) string {
	detailed := strings.TrimSpace(res.DetailedReport)
	if detailed == "" {
		return ""
	}
	if idx := strings.IndexRune(detailed, '\n'); idx > 0 {
		detailed = detailed[:idx]
	}
	if len(detailed) > 320 {
		detailed = detailed[:320]
	}
	return detailed
}

func snapshotToEpisode(snapshot agentcore.EpisodicSnapshot) (store.Episode, error) {
	if strings.TrimSpace(snapshot.RunID) == "" {
		return store.Episode{}, fmt.Errorf("run_id required")
	}
	if strings.TrimSpace(snapshot.TopicID) == "" {
		return store.Episode{}, fmt.Errorf("topic_id required")
	}
	if strings.TrimSpace(snapshot.UserID) == "" {
		return store.Episode{}, fmt.Errorf("user_id required")
	}
	ep := store.Episode{
		RunID:        snapshot.RunID,
		TopicID:      snapshot.TopicID,
		UserID:       snapshot.UserID,
		Thought:      snapshot.Thought,
		PlanDocument: snapshot.PlanDocument,
		PlanPrompt:   snapshot.PlanPrompt,
		Result:       snapshot.Result,
	}
	if len(snapshot.PlanRaw) > 0 {
		ep.PlanRaw = append([]byte(nil), snapshot.PlanRaw...)
	}
	if len(snapshot.Steps) > 0 {
		steps := make([]store.EpisodeStep, len(snapshot.Steps))
		for i, step := range snapshot.Steps {
			steps[i] = store.EpisodeStep{
				StepIndex:     step.StepIndex,
				Task:          step.Task,
				InputSnapshot: step.InputSnapshot,
				Prompt:        step.Prompt,
				Result:        step.Result,
				Artifacts:     step.Artifacts,
			}
			if !step.StartedAt.IsZero() {
				started := step.StartedAt
				steps[i].StartedAt = &started
			}
			if !step.CompletedAt.IsZero() {
				completed := step.CompletedAt
				steps[i].CompletedAt = &completed
			}
		}
		ep.Steps = steps
	}
	return ep, nil
}

// Ensure Manager satisfies the service.Manager interface.
var _ memorysvc.Manager = (*Manager)(nil)

// SnapshotToEpisode converts an episodic snapshot into the store representation.
func SnapshotToEpisode(snapshot agentcore.EpisodicSnapshot) (store.Episode, error) {
	return snapshotToEpisode(snapshot)
}
