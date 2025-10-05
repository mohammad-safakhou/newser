package semantic

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// QueryExpectation captures the desired relevance set for a semantic memory query.
type QueryExpectation struct {
	Query         string   `json:"query"`
	TopicID       string   `json:"topic_id,omitempty"`
	RelevantRuns  []string `json:"relevant_runs,omitempty"`
	RelevantTasks []string `json:"relevant_tasks,omitempty"`
}

// QueryEvaluation summarises metrics for a single query execution.
type QueryEvaluation struct {
	Query          string        `json:"query"`
	TopicID        string        `json:"topic_id,omitempty"`
	Recall         float64       `json:"recall"`
	PlanRecall     float64       `json:"plan_recall,omitempty"`
	Latency        time.Duration `json:"latency"`
	PlanLatency    time.Duration `json:"plan_latency,omitempty"`
	Hits           []string      `json:"hits"`
	PlanHits       []string      `json:"plan_hits,omitempty"`
	MissedRuns     []string      `json:"missed_runs,omitempty"`
	MissedPlanHits []string      `json:"missed_plan_hits,omitempty"`
}

// EvaluationSummary aggregates recall/latency across all expectations.
type EvaluationSummary struct {
	Results              []QueryEvaluation `json:"results"`
	MeanRecall           float64           `json:"mean_recall"`
	MeanPlanRecall       float64           `json:"mean_plan_recall"`
	MeanLatency          time.Duration     `json:"mean_latency"`
	MeanPlanLatency      time.Duration     `json:"mean_plan_latency"`
	QueriesEvaluated     int               `json:"queries_evaluated"`
	PlanQueriesEvaluated int               `json:"plan_queries_evaluated"`
}

// runSearcher abstracts the store search methods required for evaluation.
type runSearcher interface {
	SearchRunEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.RunEmbeddingSearchResult, error)
	SearchPlanStepEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.PlanStepEmbeddingSearchResult, error)
}

// Evaluate executes semantic queries and computes recall/latency statistics. It returns an
// error if embeddings cannot be generated or the store searches fail.
func Evaluate(ctx context.Context, cfg config.SemanticMemoryConfig, provider agentcore.LLMProvider, searcher runSearcher, expectations []QueryExpectation) (EvaluationSummary, error) {
	if !cfg.Enabled {
		return EvaluationSummary{}, fmt.Errorf("semantic memory disabled")
	}
	if provider == nil {
		return EvaluationSummary{}, fmt.Errorf("embedding provider not configured")
	}
	if len(expectations) == 0 {
		return EvaluationSummary{}, fmt.Errorf("no query expectations supplied")
	}
	topK := cfg.SearchTopK
	if topK <= 0 {
		topK = 5
	}
	threshold := cfg.SearchThreshold
	var summary EvaluationSummary

	for _, exp := range expectations {
		q := strings.TrimSpace(exp.Query)
		if q == "" {
			continue
		}
		embedStart := time.Now()
		vectors, err := provider.Embed(ctx, cfg.EmbeddingModel, []string{q})
		if err != nil {
			return EvaluationSummary{}, fmt.Errorf("embed query %q: %w", q, err)
		}
		if len(vectors) == 0 {
			return EvaluationSummary{}, fmt.Errorf("embed query %q: provider returned no vectors", q)
		}
		vec := vectors[0]
		searchStart := time.Now()
		hits, err := searcher.SearchRunEmbeddings(ctx, exp.TopicID, vec, topK, threshold)
		if err != nil {
			return EvaluationSummary{}, fmt.Errorf("search runs for %q: %w", q, err)
		}
		latency := time.Since(searchStart)
		eval := QueryEvaluation{Query: exp.Query, TopicID: exp.TopicID, Latency: latency}
		eval.Hits = extractRunIDs(hits)
		eval.Recall, eval.MissedRuns = computeRecall(exp.RelevantRuns, eval.Hits)

		// Optional plan-step evaluation.
		if len(exp.RelevantTasks) > 0 {
			planStart := time.Now()
			planHits, err := searcher.SearchPlanStepEmbeddings(ctx, exp.TopicID, vec, topK, threshold)
			if err != nil {
				return EvaluationSummary{}, fmt.Errorf("search plan steps for %q: %w", q, err)
			}
			eval.PlanLatency = time.Since(planStart)
			eval.PlanHits = extractTaskIDs(planHits)
			eval.PlanRecall, eval.MissedPlanHits = computeRecall(exp.RelevantTasks, eval.PlanHits)
			summary.MeanPlanLatency += eval.PlanLatency
			if !isNaN(eval.PlanRecall) {
				summary.MeanPlanRecall += eval.PlanRecall
				summary.PlanQueriesEvaluated++
			}
		}

		summary.Results = append(summary.Results, eval)
		summary.MeanLatency += eval.Latency
		if !isNaN(eval.Recall) {
			summary.MeanRecall += eval.Recall
			summary.QueriesEvaluated++
		}
		// include embedding generation time in latency metrics if needed later
		_ = embedStart
	}

	if summary.QueriesEvaluated > 0 {
		summary.MeanRecall /= float64(summary.QueriesEvaluated)
		summary.MeanLatency /= time.Duration(summary.QueriesEvaluated)
	}
	if summary.PlanQueriesEvaluated > 0 {
		summary.MeanPlanRecall /= float64(summary.PlanQueriesEvaluated)
		summary.MeanPlanLatency /= time.Duration(summary.PlanQueriesEvaluated)
	}
	return summary, nil
}

func extractRunIDs(results []store.RunEmbeddingSearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, hit := range results {
		ids = append(ids, hit.RunID)
	}
	return ids
}

func extractTaskIDs(results []store.PlanStepEmbeddingSearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, hit := range results {
		compound := fmt.Sprintf("%s:%s", hit.RunID, hit.TaskID)
		ids = append(ids, compound)
	}
	return ids
}

func computeRecall(relevant, hits []string) (float64, []string) {
	if len(relevant) == 0 {
		return 0, nil
	}
	relevantSet := make(map[string]struct{}, len(relevant))
	for _, id := range relevant {
		relevantSet[strings.TrimSpace(id)] = struct{}{}
	}
	hitSet := make(map[string]struct{}, len(hits))
	for _, id := range hits {
		hitSet[strings.TrimSpace(id)] = struct{}{}
	}
	var matches int
	var missed []string
	for id := range relevantSet {
		if _, ok := hitSet[id]; ok {
			matches++
		} else {
			missed = append(missed, id)
		}
	}
	sort.Strings(missed)
	recall := float64(matches) / float64(len(relevantSet))
	return recall, missed
}

func isNaN(v float64) bool { return v != v }
