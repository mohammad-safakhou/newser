package budget

import "testing"

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
