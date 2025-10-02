package server

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type fakePlanStore struct {
	saved   store.PlanGraphRecord
	saveErr error
	record  store.PlanGraphRecord
	found   bool
	getErr  error
}

func (f *fakePlanStore) SavePlanGraph(ctx context.Context, rec store.PlanGraphRecord) error {
	f.saved = rec
	return f.saveErr
}

func (f *fakePlanStore) GetLatestPlanGraph(ctx context.Context, thoughtID string) (store.PlanGraphRecord, bool, error) {
	return f.record, f.found, f.getErr
}

func TestPlanRepositorySaveAssignsPlanID(t *testing.T) {
	fake := &fakePlanStore{}
	repo := &planRepository{store: fake}

	doc := &planner.PlanDocument{
		Version: "v1",
		Tasks:   []planner.PlanTask{{ID: "t1", Type: "research"}},
	}

	planID, err := repo.SavePlanGraph(context.Background(), "thought-123", doc, nil)
	if err != nil {
		t.Fatalf("SavePlanGraph: %v", err)
	}
	if planID == "" {
		t.Fatalf("expected plan ID to be set")
	}
	if fake.saved.PlanID != planID {
		t.Fatalf("saved record plan_id mismatch: %s vs %s", fake.saved.PlanID, planID)
	}
	if len(fake.saved.PlanJSON) == 0 {
		t.Fatalf("expected plan json to be stored")
	}
}

func TestPlanRepositoryGetLatestPlanGraph(t *testing.T) {
	fake := &fakePlanStore{
		record: store.PlanGraphRecord{
			PlanID:         "plan-1",
			ThoughtID:      "thought-1",
			Version:        "v1",
			Confidence:     0.7,
			ExecutionOrder: []string{"t1"},
			PlanJSON:       []byte(`{"version":"v1","tasks":[{"id":"t1","type":"research"}]}`),
			UpdatedAt:      time.Now(),
		},
		found: true,
	}
	repo := &planRepository{store: fake}

	stored, ok, err := repo.GetLatestPlanGraph(context.Background(), "thought-1")
	if err != nil || !ok {
		t.Fatalf("GetLatestPlanGraph: %v ok=%v", err, ok)
	}
	if stored.PlanID != "plan-1" {
		t.Fatalf("expected plan_id plan-1, got %s", stored.PlanID)
	}
	if stored.Document == nil || stored.Document.Version != "v1" {
		t.Fatalf("expected document decoded")
	}
}

func TestPlanRepositoryPropagatesErrors(t *testing.T) {
	fake := &fakePlanStore{saveErr: errors.New("boom")}
	repo := &planRepository{store: fake}

	doc := &planner.PlanDocument{Version: "v1", Tasks: []planner.PlanTask{{ID: "t1", Type: "research"}}}
	if _, err := repo.SavePlanGraph(context.Background(), "thought", doc, []byte(`{"version":"v1"}`)); err == nil {
		t.Fatalf("expected error")
	}

	fake.getErr = errors.New("not found")
	_, _, err := repo.GetLatestPlanGraph(context.Background(), "thought")
	if err == nil {
		t.Fatalf("expected error from get")
	}
}

var _ agentcore.PlanRepository = (*planRepository)(nil)
