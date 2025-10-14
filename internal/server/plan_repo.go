package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type planStore interface {
	SavePlanGraph(ctx context.Context, rec store.PlanGraphRecord) error
	GetLatestPlanGraph(ctx context.Context, thoughtID string) (store.PlanGraphRecord, bool, error)
}

type planRepository struct {
	store  planStore
	logger *log.Logger
}

func newPlanRepository(st *store.Store, logger *log.Logger) agentcore.PlanRepository {
	if st == nil {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[PLAN-REPO] ", log.LstdFlags)
	}
	return &planRepository{store: st, logger: logger}
}

func (r *planRepository) SavePlanGraph(ctx context.Context, thoughtID string, doc *planner.PlanDocument, raw []byte) (string, error) {
	if doc == nil {
		return "", fmt.Errorf("plan document is nil")
	}

	var (
		normalizedDoc  *planner.PlanDocument
		normalizedJSON []byte
		err            error
	)
	if len(raw) > 0 {
		normalizedDoc, normalizedJSON, err = planner.NormalizePlanDocument(raw)
		if err != nil {
			return "", err
		}
	} else {
		encoded, err := json.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("marshal plan doc: %w", err)
		}
		normalizedDoc, normalizedJSON, err = planner.NormalizePlanDocument(encoded)
		if err != nil {
			return "", err
		}
	}
	*doc = *normalizedDoc
	raw = normalizedJSON
	planID := doc.PlanID
	if planID == "" {
		if thoughtID != "" {
			planID = thoughtID
		} else {
			planID = uuid.New().String()
		}
		doc.PlanID = planID
	}
	if doc.Version == "" {
		doc.Version = "v1"
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		return planID, fmt.Errorf("marshal plan doc: %w", err)
	}
	raw = encoded

	budgetBytes, err := json.Marshal(doc.Budget)
	if err != nil {
		return planID, fmt.Errorf("marshal budget: %w", err)
	}
	estimatesBytes, err := json.Marshal(doc.Estimates)
	if err != nil {
		return planID, fmt.Errorf("marshal estimates: %w", err)
	}

	rec := store.PlanGraphRecord{
		PlanID:         planID,
		ThoughtID:      thoughtID,
		Version:        doc.Version,
		Confidence:     doc.Confidence,
		ExecutionOrder: doc.ExecutionOrder,
		Budget:         budgetBytes,
		Estimates:      estimatesBytes,
		PlanJSON:       raw,
	}
	if err := r.store.SavePlanGraph(ctx, rec); err != nil {
		return planID, err
	}
	return planID, nil
}

func (r *planRepository) GetLatestPlanGraph(ctx context.Context, thoughtID string) (agentcore.StoredPlanGraph, bool, error) {
	rec, ok, err := r.store.GetLatestPlanGraph(ctx, thoughtID)
	if err != nil || !ok {
		return agentcore.StoredPlanGraph{}, ok, err
	}
	var doc planner.PlanDocument
	if err := json.Unmarshal(rec.PlanJSON, &doc); err != nil {
		return agentcore.StoredPlanGraph{}, false, fmt.Errorf("decode plan graph: %w", err)
	}
	if doc.PlanID == "" {
		doc.PlanID = rec.PlanID
	}
	stored := agentcore.StoredPlanGraph{
		PlanID:    rec.PlanID,
		ThoughtID: rec.ThoughtID,
		Document:  &doc,
		RawJSON:   rec.PlanJSON,
		UpdatedAt: rec.UpdatedAt,
	}
	return stored, true, nil
}
