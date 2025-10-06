package manifest

import (
	"testing"
	"time"

	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func TestBuildRunManifest(t *testing.T) {
	ep := store.Episode{
		RunID:        "run-1",
		TopicID:      "topic-1",
		UserID:       "user-1",
		Thought:      core.UserThought{ID: "run-1", TopicID: "topic-1", UserID: "user-1", Content: "Summarise"},
		PlanPrompt:   "plan prompt",
		PlanDocument: &planner.PlanDocument{Version: "v1"},
		Result: core.ProcessingResult{
			ID:             "run-1",
			Summary:        "Summary",
			DetailedReport: "Detailed report",
			Confidence:     0.8,
			AgentsUsed:     []string{"research", "analysis"},
			LLMModelsUsed:  []string{"gpt"},
			CostEstimate:   12.34,
			TokensUsed:     4200,
			Sources: []core.Source{
				{ID: "src-1", Title: "Example", URL: "https://example.com/a", Summary: "Example summary", Type: "news", Credibility: 0.8},
			},
			Metadata: map[string]any{
				"items": []map[string]any{
					{"title": "Claim", "source_ids": []string{"src-1"}},
				},
				"digest_stats": map[string]any{"count": 1},
				"budget_usage": map[string]any{"cost_used": 12.34},
			},
		},
		CreatedAt: time.Unix(10, 0),
	}

	payload, err := BuildRunManifest(ep)
	if err != nil {
		t.Fatalf("BuildRunManifest: %v", err)
	}
	if payload.Version != RunManifestVersion {
		t.Fatalf("unexpected version: %s", payload.Version)
	}
	if payload.RunID != "run-1" || payload.TopicID != "topic-1" {
		t.Fatalf("unexpected identifiers: %+v", payload)
	}
	if len(payload.Sources) != 1 || payload.Sources[0].ID != "src-1" {
		t.Fatalf("sources not propagated: %+v", payload.Sources)
	}
	if payload.Digest == nil {
		t.Fatalf("digest stats missing")
	}
	switch v := payload.Digest["count"].(type) {
	case int:
		if v != 1 {
			t.Fatalf("unexpected digest count: %v", v)
		}
	case float64:
		if v != 1 {
			t.Fatalf("unexpected digest count: %v", v)
		}
	default:
		t.Fatalf("unexpected digest count type: %T", v)
	}
	if payload.Budget == nil || payload.Budget["cost_used"].(float64) != 12.34 {
		t.Fatalf("budget metadata missing: %+v", payload.Budget)
	}
	if len(payload.Result.Items) != 1 {
		t.Fatalf("expected manifest items copied")
	}
	if payload.Plan == nil || payload.Plan.Document == nil {
		t.Fatalf("expected plan document to be included")
	}
}

func TestSignAndVerifyRunManifest(t *testing.T) {
	payload := RunManifestPayload{Version: RunManifestVersion, RunID: "run", TopicID: "topic", UserID: "user", CreatedAt: time.Now().UTC()}
	signed, err := SignRunManifest(payload, "secret", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("SignRunManifest: %v", err)
	}
	if signed.Checksum == "" || signed.Signature == "" {
		t.Fatalf("expected checksum and signature to be populated: %+v", signed)
	}
	if err := VerifyRunManifest(signed, "secret"); err != nil {
		t.Fatalf("VerifyRunManifest unexpected error: %v", err)
	}
	if err := VerifyRunManifest(signed, "wrong"); err == nil {
		t.Fatalf("expected signature mismatch")
	}
}
