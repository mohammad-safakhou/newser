package budget

import (
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/internal/planner"
)

func TestConfigValidate(t *testing.T) {
	neg := float64(-1)
	cfg := Config{MaxCost: &neg}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error")
	}

	cost := float64(10)
	threshold := float64(20)
	cfg = Config{MaxCost: &cost, ApprovalThreshold: &threshold}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected threshold validation error")
	}
}

func TestMergeClone(t *testing.T) {
	cost := float64(5)
	base := Config{MaxCost: &cost, RequireApproval: false, Metadata: map[string]interface{}{"team": "core"}}
	override := Config{RequireApproval: true, Metadata: map[string]interface{}{"team": "ops"}}
	merged := Merge(base, override)
	if !merged.RequireApproval {
		t.Fatalf("expected require approval flag")
	}
	if merged.Metadata["team"].(string) != "ops" {
		t.Fatalf("expected metadata override")
	}
	if merged.MaxCost == nil || *merged.MaxCost != cost {
		t.Fatalf("expected max cost to persist")
	}
	// ensure clone
	merged.Metadata["team"] = "changed"
	if base.Metadata["team"].(string) != "core" {
		t.Fatalf("metadata should be isolated from base")
	}
}

func TestMonitorAddAndTime(t *testing.T) {
	maxCost := 5.0
	maxTokens := int64(1000)
	maxTime := int64(1)
	cfg := Config{MaxCost: &maxCost, MaxTokens: &maxTokens, MaxTimeSeconds: &maxTime}
	mon := NewMonitor(cfg)
	if err := mon.Add(2.5, 400); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mon.Add(3.0, 700); err == nil {
		t.Fatalf("expected token budget breach")
	}
}

func TestRequiresApproval(t *testing.T) {
	cfg := Config{}
	if RequiresApproval(cfg, 5) {
		t.Fatalf("unexpected approval requirement")
	}
	threshold := 4.0
	cfg.ApprovalThreshold = &threshold
	if !RequiresApproval(cfg, 5) {
		t.Fatalf("expected approval requirement when exceeding threshold")
	}
}

func TestEstimateFromPlan(t *testing.T) {
	plan := &planner.PlanDocument{
		Tasks: []planner.PlanTask{{EstimatedCost: 1.5, EstimatedTokens: 120, EstimatedTime: "2m"}},
	}
	est := EstimateFromPlan(plan)
	if est.Cost == 0 || est.Tokens == 0 || est.Duration == 0 {
		t.Fatalf("expected task-level estimates to populate fields: %+v", est)
	}

	plan.Estimates = &planner.PlanEstimates{TotalCost: 3.2, TotalTokens: 250, TotalTime: "5m"}
	est = EstimateFromPlan(plan)
	if est.Cost != 3.2 || est.Tokens != 250 || est.Duration != 5*time.Minute {
		t.Fatalf("expected aggregate estimates, got %+v", est)
	}
}

func TestSerializeHelpers(t *testing.T) {
	cost := 9.5
	tokens := int64(500)
	seconds := int64(600)
	cfg := Config{MaxCost: &cost, MaxTokens: &tokens, MaxTimeSeconds: &seconds, RequireApproval: true, Metadata: map[string]interface{}{"team": "news"}}
	est := Estimate{Cost: 5.5, Tokens: 200, Duration: 3 * time.Minute}
	usage := Usage{Cost: 6.1, Tokens: 210, Elapsed: 4 * time.Minute}

	report := BuildReport(cfg, est, usage)
	if report["config"].(map[string]interface{})["require_approval"].(bool) != true {
		t.Fatalf("expected require approval in report")
	}
	if report["estimate"].(map[string]interface{})["tokens"].(int64) != int64(200) {
		t.Fatalf("expected estimate tokens to match")
	}
	if report["usage"].(map[string]interface{})["cost"].(float64) != usage.Cost {
		t.Fatalf("expected usage cost to match")
	}

	exceeded := ErrExceeded{Kind: "cost", Usage: "usage", Limit: "limit"}
	reason := FormatBreachReason(cfg, usage, exceeded)
	if reason == "" {
		t.Fatalf("expected formatted breach reason")
	}
}
