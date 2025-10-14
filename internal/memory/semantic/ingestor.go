package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// Ingestor coordinates semantic memory embeddings for completed runs.
type Ingestor struct {
	store           *store.Store
	provider        agentcore.LLMProvider
	cfg             config.SemanticMemoryConfig
	logger          *log.Logger
	searchLatency   otelmetric.Float64Histogram
	searchHits      otelmetric.Int64Histogram
	searchMisses    otelmetric.Int64Counter
	embeddingDrift  otelmetric.Int64Counter
	rebuildCounter  otelmetric.Int64Counter
	runIngestCount  otelmetric.Int64Counter
	planIngestCount otelmetric.Int64Counter
}

// NewIngestor builds a semantic memory ingestor. Returns nil when disabled.
func NewIngestor(st *store.Store, provider agentcore.LLMProvider, cfg config.SemanticMemoryConfig, logger *log.Logger) (*Ingestor, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if provider == nil {
		return nil, errors.New("semantic memory requires an embedding-capable LLM provider")
	}
	if cfg.EmbeddingModel == "" {
		return nil, errors.New("semantic memory embedding_model must be configured")
	}
	if cfg.EmbeddingDimensions <= 0 {
		cfg.EmbeddingDimensions = store.DefaultEmbeddingDimensions
	}
	if cfg.WriterBatchSize <= 0 {
		cfg.WriterBatchSize = 32
	}
	if cfg.SearchTopK <= 0 {
		cfg.SearchTopK = 5
	}
	if cfg.SearchThreshold <= 0 {
		cfg.SearchThreshold = 0.8
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[SEMANTIC] ", log.LstdFlags)
	}
	ing := &Ingestor{store: st, provider: provider, cfg: cfg, logger: logger}
	meter := otel.Meter("memory/semantic")
	var err error
	ing.searchLatency, err = meter.Float64Histogram("semantic_search_latency_ms", otelmetric.WithUnit("ms"))
	if err != nil {
		logger.Printf("otel histogram semantic_search_latency_ms: %v", err)
	}
	ing.searchHits, err = meter.Int64Histogram("semantic_search_hits", otelmetric.WithUnit("1"))
	if err != nil {
		logger.Printf("otel histogram semantic_search_hits: %v", err)
	}
	ing.searchMisses, err = meter.Int64Counter("semantic_search_misses")
	if err != nil {
		logger.Printf("otel counter semantic_search_misses: %v", err)
	}
	ing.embeddingDrift, err = meter.Int64Counter("semantic_embedding_drift")
	if err != nil {
		logger.Printf("otel counter semantic_embedding_drift: %v", err)
	}
	ing.rebuildCounter, err = meter.Int64Counter("semantic_rebuild_events")
	if err != nil {
		logger.Printf("otel counter semantic_rebuild_events: %v", err)
	}
	ing.runIngestCount, err = meter.Int64Counter("semantic_ingest_runs")
	if err != nil {
		logger.Printf("otel counter semantic_ingest_runs: %v", err)
	}
	ing.planIngestCount, err = meter.Int64Counter("semantic_ingest_plan_steps")
	if err != nil {
		logger.Printf("otel counter semantic_ingest_plan_steps: %v", err)
	}
	return ing, nil
}

// IngestRun embeds run-level and plan-step artifacts for later semantic retrieval.
func (i *Ingestor) IngestRun(ctx context.Context, topicID, runID string, result agentcore.ProcessingResult, plan *planner.PlanDocument) error {
	if i == nil {
		return nil
	}
	if runID == "" || topicID == "" {
		return fmt.Errorf("ingest requires run/topic identifiers")
	}

	if err := i.embedRunSummary(ctx, topicID, runID, result); err != nil {
		return err
	}
	if plan != nil && len(plan.Tasks) > 0 {
		if err := i.embedPlanSteps(ctx, topicID, runID, plan); err != nil {
			return err
		}
	} else {
		if err := i.store.ReplacePlanStepEmbeddings(ctx, runID, nil); err != nil {
			i.logger.Printf("warn: clear plan step embeddings failed: %v", err)
		}
	}
	return nil
}

func (i *Ingestor) embedRunSummary(ctx context.Context, topicID, runID string, result agentcore.ProcessingResult) error {
	text := buildRunSummaryText(result)
	vectors, err := i.provider.Embed(ctx, i.cfg.EmbeddingModel, []string{text})
	if err != nil {
		return fmt.Errorf("embed run summary: %w", err)
	}
	if len(vectors) == 0 {
		return fmt.Errorf("embed run summary: provider returned no vectors")
	}
	vec := vectors[0]
	if i.cfg.EmbeddingDimensions > 0 && len(vec) != i.cfg.EmbeddingDimensions {
		i.logger.Printf("warn: embedding dimensions mismatch (got %d want %d)", len(vec), i.cfg.EmbeddingDimensions)
		if i.embeddingDrift != nil {
			i.embeddingDrift.Add(ctx, 1)
		}
	}
	metadata := map[string]interface{}{
		"model":      i.cfg.EmbeddingModel,
		"source":     "run_summary",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	if result.Metadata != nil {
		if stats, ok := result.Metadata["budget_usage"].(map[string]interface{}); ok {
			metadata["budget_usage"] = stats
		}
	}
	if snippet := summarySnippet(result); snippet != "" {
		metadata["summary_snippet"] = snippet
	}
	rec := store.RunEmbeddingRecord{
		RunID:    runID,
		TopicID:  topicID,
		Kind:     "run_summary",
		Vector:   vec,
		Metadata: metadata,
	}
	if err := i.store.UpsertRunEmbedding(ctx, rec); err != nil {
		return fmt.Errorf("store run embedding: %w", err)
	}
	if i.runIngestCount != nil {
		i.runIngestCount.Add(ctx, 1)
	}
	return nil
}

// SearchSimilar executes semantic similarity queries for planning and UI consumers.
func (i *Ingestor) SearchSimilar(ctx context.Context, req agentcore.SemanticSearchRequest) (agentcore.SemanticSearchResults, error) {
	var empty agentcore.SemanticSearchResults
	if i == nil {
		return empty, fmt.Errorf("semantic memory disabled")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return empty, nil
	}
	topK := req.TopK
	if topK <= 0 {
		topK = i.cfg.SearchTopK
		if topK <= 0 {
			topK = 5
		}
	}
	threshold := req.Threshold
	if threshold <= 0 {
		threshold = i.cfg.SearchThreshold
		if threshold <= 0 {
			threshold = 0.8
		}
	}
	vectors, err := i.provider.Embed(ctx, i.cfg.EmbeddingModel, []string{query})
	if err != nil {
		return empty, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) == 0 {
		return empty, nil
	}
	vec := vectors[0]
	results := agentcore.SemanticSearchResults{}
	searchStart := time.Now()
	runHits, err := i.store.SearchRunEmbeddings(ctx, req.TopicID, vec, topK, threshold)
	if err != nil {
		return empty, fmt.Errorf("search runs: %w", err)
	}
	latency := time.Since(searchStart)
	if i.searchLatency != nil {
		i.searchLatency.Record(ctx, latency.Seconds()*1000, otelmetric.WithAttributes(attribute.String("search.type", "run")))
	}
	for _, hit := range runHits {
		results.Runs = append(results.Runs, agentcore.SemanticRunMatch{
			RunID:      hit.RunID,
			TopicID:    hit.TopicID,
			Kind:       hit.Kind,
			Distance:   hit.Distance,
			Similarity: similarityFromDistance(hit.Distance),
			Metadata:   hit.Metadata,
			CreatedAt:  hit.CreatedAt,
		})
	}
	if i.searchHits != nil {
		i.searchHits.Record(ctx, int64(len(results.Runs)), otelmetric.WithAttributes(attribute.String("hit.kind", "run")))
	}
	if len(results.Runs) == 0 && i.searchMisses != nil {
		i.searchMisses.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("hit.kind", "run")))
	}
	if req.IncludeSteps {
		planStart := time.Now()
		planHits, err := i.store.SearchPlanStepEmbeddings(ctx, req.TopicID, vec, topK, threshold)
		if err != nil {
			return empty, fmt.Errorf("search plan steps: %w", err)
		}
		latency := time.Since(planStart)
		if i.searchLatency != nil {
			i.searchLatency.Record(ctx, latency.Seconds()*1000, otelmetric.WithAttributes(attribute.String("search.type", "plan")))
		}
		for _, hit := range planHits {
			results.PlanSteps = append(results.PlanSteps, agentcore.SemanticPlanMatch{
				RunID:      hit.RunID,
				TopicID:    hit.TopicID,
				TaskID:     hit.TaskID,
				Kind:       hit.Kind,
				Distance:   hit.Distance,
				Similarity: similarityFromDistance(hit.Distance),
				Metadata:   hit.Metadata,
				CreatedAt:  hit.CreatedAt,
			})
		}
		if i.searchHits != nil {
			i.searchHits.Record(ctx, int64(len(results.PlanSteps)), otelmetric.WithAttributes(attribute.String("hit.kind", "plan")))
		}
		if len(results.PlanSteps) == 0 && i.searchMisses != nil {
			i.searchMisses.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("hit.kind", "plan")))
		}
	}
	return results, nil
}

func (i *Ingestor) embedPlanSteps(ctx context.Context, topicID, runID string, plan *planner.PlanDocument) error {
	tasks := plan.Tasks
	if len(tasks) == 0 {
		return nil
	}

	batchSize := i.cfg.WriterBatchSize
	inputs := make([]string, 0, len(tasks))
	meta := make([]store.PlanStepEmbeddingRecord, 0, len(tasks))
	for _, task := range tasks {
		text := buildPlanTaskText(task)
		inputs = append(inputs, text)
		meta = append(meta, store.PlanStepEmbeddingRecord{
			RunID:   runID,
			TopicID: topicID,
			TaskID:  task.ID,
			Kind:    task.Type,
			Metadata: map[string]interface{}{
				"type":        task.Type,
				"name":        task.Name,
				"description": task.Description,
			},
		})
	}

	vectors := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		chunk := inputs[start:end]
		resp, err := i.provider.Embed(ctx, i.cfg.EmbeddingModel, chunk)
		if err != nil {
			return fmt.Errorf("embed plan steps: %w", err)
		}
		if len(resp) != len(chunk) {
			return fmt.Errorf("embed plan steps: expected %d vectors, got %d", len(chunk), len(resp))
		}
		vectors = append(vectors, resp...)
	}
	if len(vectors) != len(meta) {
		return fmt.Errorf("embed plan steps: expected %d vectors, got %d", len(meta), len(vectors))
	}
	records := make([]store.PlanStepEmbeddingRecord, len(meta))
	for idx := range meta {
		records[idx] = meta[idx]
		records[idx].Vector = vectors[idx]
	}
	if err := i.store.ReplacePlanStepEmbeddings(ctx, runID, records); err != nil {
		return fmt.Errorf("store plan step embeddings: %w", err)
	}
	if i.planIngestCount != nil {
		i.planIngestCount.Add(ctx, int64(len(records)))
	}
	if i.cfg.RebuildOnStartup && i.rebuildCounter != nil {
		i.rebuildCounter.Add(ctx, 1)
	}
	return nil
}

func buildRunSummaryText(result agentcore.ProcessingResult) string {
	var b strings.Builder
	b.WriteString("Run Summary\n")
	if result.UserThought.Content != "" {
		b.WriteString("Thought: ")
		b.WriteString(strings.TrimSpace(result.UserThought.Content))
		b.WriteString("\n\n")
	}
	if result.Summary != "" {
		b.WriteString("Summary:\n")
		b.WriteString(strings.TrimSpace(result.Summary))
		b.WriteString("\n\n")
	}
	if result.DetailedReport != "" {
		detailed := strings.TrimSpace(result.DetailedReport)
		if len(detailed) > 1500 {
			detailed = detailed[:1500]
		}
		b.WriteString("Details:\n")
		b.WriteString(detailed)
		b.WriteString("\n\n")
	}
	if len(result.Highlights) > 0 {
		b.WriteString("Highlights:\n")
		for _, h := range result.Highlights {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(h.Title))
			if h.Content != "" {
				b.WriteString(": ")
				snippet := strings.TrimSpace(h.Content)
				if len(snippet) > 200 {
					snippet = snippet[:200]
				}
				b.WriteString(snippet)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(result.Sources) > 0 {
		b.WriteString("Sources:\n")
		for _, src := range result.Sources {
			if src.Title != "" {
				b.WriteString(src.Title)
				b.WriteString(" - ")
			}
			b.WriteString(src.URL)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func summarySnippet(result agentcore.ProcessingResult) string {
	if snippet := strings.TrimSpace(result.Summary); snippet != "" {
		return truncateSnippet(snippet, 240)
	}
	if len(result.Highlights) > 0 {
		if h := strings.TrimSpace(result.Highlights[0].Content); h != "" {
			return truncateSnippet(h, 200)
		}
	}
	if detailed := strings.TrimSpace(result.DetailedReport); detailed != "" {
		return truncateSnippet(detailed, 240)
	}
	return ""
}

func truncateSnippet(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
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

func buildPlanTaskText(task planner.PlanTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s [%s]\n", task.ID, task.Type)
	if task.Name != "" {
		fmt.Fprintf(&b, "Name: %s\n", task.Name)
	}
	if task.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", strings.TrimSpace(task.Description))
	}
	if len(task.Parameters) > 0 {
		params, _ := json.Marshal(task.Parameters)
		b.WriteString("Parameters: ")
		b.Write(params)
		b.WriteString("\n")
	}
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&b, "DependsOn: %s\n", strings.Join(task.DependsOn, ","))
	}
	if task.Timeout != "" {
		fmt.Fprintf(&b, "Timeout: %s\n", task.Timeout)
	}
	if task.EstimatedCost > 0 {
		fmt.Fprintf(&b, "EstimatedCost: %.2f\n", task.EstimatedCost)
	}
	return b.String()
}
