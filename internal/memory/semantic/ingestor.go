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
)

// Ingestor coordinates semantic memory embeddings for completed runs.
type Ingestor struct {
	store    *store.Store
	provider agentcore.LLMProvider
	cfg      config.SemanticMemoryConfig
	logger   *log.Logger
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
	return &Ingestor{
		store:    st,
		provider: provider,
		cfg:      cfg,
		logger:   logger,
	}, nil
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
	return nil
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
