package main

import (
	"encoding/json"
	"testing"

	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func TestExtractProceduralTemplateUsage(t *testing.T) {
	plan := &planner.PlanDocument{
		Metadata: map[string]interface{}{
			"procedural_templates": []interface{}{
				map[string]interface{}{
					"stage":       "analysis",
					"status":      "reused",
					"template_id": "tpl-123",
					"fingerprint": "abcdef1234567890",
					"occurrences": float64(5),
					"task_ids":    []interface{}{"task-a", "task-b"},
					"task_types":  []interface{}{"analysis", "synthesis"},
				},
			},
		},
	}

	usages := extractProceduralTemplateUsage(plan)
	if len(usages) != 1 {
		t.Fatalf("expected a single usage, got %d", len(usages))
	}
	usage := usages[0]
	if usage.Stage != "analysis" {
		t.Fatalf("unexpected stage: %s", usage.Stage)
	}
	if usage.Status != "reused" {
		t.Fatalf("unexpected status: %s", usage.Status)
	}
	if usage.TemplateID != "tpl-123" {
		t.Fatalf("unexpected template ID: %s", usage.TemplateID)
	}
	if usage.Occurrences != 5 {
		t.Fatalf("unexpected occurrences: %d", usage.Occurrences)
	}
	if usage.TaskCount != 2 {
		t.Fatalf("unexpected task count: %d", usage.TaskCount)
	}
	if len(usage.TaskTypes) != 2 {
		t.Fatalf("expected task types to be captured")
	}
}

func TestPlanWithMetadataFromRaw(t *testing.T) {
	plan := planner.PlanDocument{Version: "v1"}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	ep := store.Episode{PlanRaw: raw}
	loaded := planWithMetadata(ep)
	if loaded == nil {
		t.Fatalf("expected plan to be loaded from raw")
	}
	if loaded.Version != "v1" {
		t.Fatalf("unexpected version: %s", loaded.Version)
	}
}
