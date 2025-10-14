package manager

import (
	"context"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type stubStore struct {
	runSimilar     bool
	planSimilar    bool
	deltaRecords   []store.MemoryDeltaRecord
	lastJob        *store.MemoryJobRecord
	jobStatuses    []string
	jobResults     []store.MemoryJobResultRecord
	createJobError error
	completeError  error
}

func (s *stubStore) SaveEpisode(context.Context, store.Episode) error      { return nil }
func (s *stubStore) ListRuns(context.Context, string) ([]store.Run, error) { return nil, nil }
func (s *stubStore) GetEpisodeByRunID(context.Context, string) (store.Episode, bool, error) {
	return store.Episode{}, false, nil
}
func (s *stubStore) HasSimilarRunEmbedding(context.Context, string, []float32, float64, time.Duration) (bool, error) {
	return s.runSimilar, nil
}
func (s *stubStore) HasSimilarPlanStepEmbedding(context.Context, string, []float32, float64, time.Duration) (bool, error) {
	return s.planSimilar, nil
}
func (s *stubStore) LogMemoryDelta(_ context.Context, rec store.MemoryDeltaRecord) error {
	s.deltaRecords = append(s.deltaRecords, rec)
	return nil
}
func (s *stubStore) CreateMemoryJob(_ context.Context, rec store.MemoryJobRecord) (store.MemoryJobRecord, error) {
	if s.createJobError != nil {
		return store.MemoryJobRecord{}, s.createJobError
	}
	rec.ID = 1
	now := time.Now().UTC()
	if rec.StartedAt == nil {
		rec.StartedAt = &now
	}
	rec.CreatedAt = now
	rec.UpdatedAt = now
	s.lastJob = &rec
	return rec, nil
}
func (s *stubStore) CompleteMemoryJob(_ context.Context, jobID int64, status string, result store.MemoryJobResultRecord) error {
	if s.completeError != nil {
		return s.completeError
	}
	s.jobStatuses = append(s.jobStatuses, status)
	result.JobID = jobID
	s.jobResults = append(s.jobResults, result)
	return nil
}

type stubProvider struct {
	vectors [][]float32
}

func (s *stubProvider) Generate(context.Context, string, string, map[string]interface{}) (string, error) {
	return "", nil
}
func (s *stubProvider) GenerateWithTokens(context.Context, string, string, map[string]interface{}) (string, int64, int64, error) {
	return "", 0, 0, nil
}
func (s *stubProvider) Embed(ctx context.Context, model string, input []string) ([][]float32, error) {
	if len(s.vectors) > 0 {
		return s.vectors, nil
	}
	out := make([][]float32, len(input))
	for i := range input {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}
func (s *stubProvider) GetAvailableModels() []string { return nil }
func (s *stubProvider) GetModelInfo(string) (agentcore.ModelInfo, error) {
	return agentcore.ModelInfo{}, nil
}
func (s *stubProvider) CalculateCost(int64, int64, string) float64 { return 0 }

func TestManagerDeltaWithoutSemantic(t *testing.T) {
	cfg := config.MemoryConfig{}
	cfg.Episodic.Enabled = true
	store := &stubStore{}
	mgr := New(store, cfg, nil, nil)
	if mgr == nil {
		t.Fatalf("expected manager instance")
	}
	resp, err := mgr.Delta(context.Background(), memorysvc.DeltaRequest{
		TopicID: "topic",
		Items:   []memorysvc.DeltaItem{{ID: "a"}},
	})
	if err != nil {
		t.Fatalf("Delta returned error: %v", err)
	}
	if len(resp.Novel) != 1 {
		t.Fatalf("expected 1 novel item, got %d", len(resp.Novel))
	}
	if resp.DuplicateCount != 0 {
		t.Fatalf("expected no duplicates, got %d", resp.DuplicateCount)
	}
	if len(store.deltaRecords) != 1 {
		t.Fatalf("expected delta record to be logged")
	}
	if store.deltaRecords[0].NovelItems != 1 || store.deltaRecords[0].DuplicateItems != 0 {
		t.Fatalf("unexpected delta record counts: %+v", store.deltaRecords[0])
	}
}

func TestManagerDeltaSemanticDuplicate(t *testing.T) {
	cfg := config.MemoryConfig{}
	cfg.Semantic.Enabled = true
	cfg.Semantic.EmbeddingModel = "embed"
	cfg.Semantic.DeltaThreshold = 0.9
	cfg.Semantic.DeltaWindow = time.Hour
	store := &stubStore{runSimilar: true}
	provider := &stubProvider{}
	mgr := New(store, cfg, provider, nil)
	if mgr == nil {
		t.Fatalf("expected manager instance")
	}
	item := memorysvc.DeltaItem{ID: "doc-1", Payload: map[string]interface{}{"text": "breaking news"}}
	resp, err := mgr.Delta(context.Background(), memorysvc.DeltaRequest{
		TopicID: "topic",
		Items:   []memorysvc.DeltaItem{item},
	})
	if err != nil {
		t.Fatalf("Delta returned error: %v", err)
	}
	if resp.DuplicateCount != 1 {
		t.Fatalf("expected duplicate to be detected, got %d", resp.DuplicateCount)
	}
	if len(resp.Novel) != 0 {
		t.Fatalf("expected no novel items, got %d", len(resp.Novel))
	}
	if len(store.deltaRecords) != 1 {
		t.Fatalf("expected delta record logged")
	}
	if !store.deltaRecords[0].Semantic {
		t.Fatalf("expected semantic flag to be true")
	}
	if store.deltaRecords[0].DuplicateItems != 1 {
		t.Fatalf("expected duplicate count recorded")
	}
}
