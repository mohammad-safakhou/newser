package semantic

import (
	"context"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type stubEmbedProvider struct{}

type stubSearcher struct {
	runs  map[string][]store.RunEmbeddingSearchResult
	steps map[string][]store.PlanStepEmbeddingSearchResult
}

type stubVec struct{}

type stubLLMProvider interface {
	agentcore.LLMProvider
}

func (stubEmbedProvider) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) {
	return "", nil
}

func (stubEmbedProvider) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) {
	return "", 0, 0, nil
}

func (stubEmbedProvider) Embed(ctx context.Context, model string, input []string) ([][]float32, error) {
	vectors := make([][]float32, len(input))
	for i := range input {
		vectors[i] = hashToVector(input[i])
	}
	return vectors, nil
}

func (stubEmbedProvider) GetAvailableModels() []string { return []string{"stub"} }

func (stubEmbedProvider) GetModelInfo(model string) (agentcore.ModelInfo, error) {
	return agentcore.ModelInfo{Name: model}, nil
}

func (stubEmbedProvider) CalculateCost(_, _ int64, _ string) float64 { return 0 }

func hashToVector(s string) []float32 {
	vec := make([]float32, 2)
	for i, r := range s {
		vec[i%2] += float32(r % 7)
	}
	return vec
}

func (s stubSearcher) SearchRunEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.RunEmbeddingSearchResult, error) {
	key := keyFromVector(vector)
	return s.runs[key], nil
}

func (s stubSearcher) SearchPlanStepEmbeddings(ctx context.Context, topicID string, vector []float32, topK int, threshold float64) ([]store.PlanStepEmbeddingSearchResult, error) {
	key := keyFromVector(vector)
	return s.steps[key], nil
}

func keyFromVector(vec []float32) string {
	return string(rune(int(vec[0]))) + string(rune(int(vec[1])))
}

func TestEvaluateComputesRecall(t *testing.T) {
	provider := stubEmbedProvider{}
	now := time.Now()
	searcher := stubSearcher{
		runs: map[string][]store.RunEmbeddingSearchResult{
			keyFromVector(hashToVector("supply chain")): {
				{RunID: "run-1", TopicID: "topic", Kind: "run_summary", Distance: 0.1, Metadata: map[string]interface{}{"score": 0.9}, CreatedAt: now},
				{RunID: "run-2", TopicID: "topic", Kind: "run_summary", Distance: 0.5, Metadata: map[string]interface{}{"score": 0.5}, CreatedAt: now},
			},
		},
		steps: map[string][]store.PlanStepEmbeddingSearchResult{
			keyFromVector(hashToVector("supply chain")): {
				{RunID: "run-1", TopicID: "topic", TaskID: "task-1", Kind: "analysis", Distance: 0.2, Metadata: map[string]interface{}{"type": "analysis"}, CreatedAt: now},
				{RunID: "run-3", TopicID: "topic", TaskID: "task-3", Kind: "research", Distance: 0.6, Metadata: map[string]interface{}{"type": "research"}, CreatedAt: now},
			},
		},
	}

	cfg := config.SemanticMemoryConfig{
		Enabled:             true,
		EmbeddingModel:      "stub-embedding",
		EmbeddingDimensions: 2,
		SearchTopK:          5,
		SearchThreshold:     0.9,
	}

	expectations := []QueryExpectation{{
		Query:         "supply chain",
		TopicID:       "topic",
		RelevantRuns:  []string{"run-1", "run-2", "run-4"},
		RelevantTasks: []string{"run-1:task-1"},
	}}

	summary, err := Evaluate(context.Background(), cfg, provider, searcher, expectations)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Recall != 2.0/3.0 {
		t.Fatalf("expected recall 2/3, got %f", result.Recall)
	}
	if result.PlanRecall != 1.0 {
		t.Fatalf("expected plan recall 1.0, got %f", result.PlanRecall)
	}
}
