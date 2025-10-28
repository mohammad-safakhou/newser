package config

import "testing"

func TestFairnessNormalize(t *testing.T) {
	cfg := FairnessConfig{
		BaselineCredibility: 1.5,
		MinCredibility:      -0.2,
		BiasAdjustmentRange: 2,
		DomainBias:          map[string]float64{" Example.COM ": 2, "bad": -5, "": 0.5},
		Alerts:              FairnessAlerts{LowCredibility: -0.5, HighBias: 1.5},
	}

	norm := cfg.Normalize()
	if norm.BaselineCredibility != 1 {
		t.Fatalf("expected baseline to clamp to 1, got %.2f", norm.BaselineCredibility)
	}
	if norm.MinCredibility != 0 {
		t.Fatalf("expected min credibility to clamp to 0, got %.2f", norm.MinCredibility)
	}
	if norm.BiasAdjustmentRange != 1 {
		t.Fatalf("expected bias range to clamp to 1, got %.2f", norm.BiasAdjustmentRange)
	}
	if len(norm.DomainBias) != 2 {
		t.Fatalf("expected 2 domain entries, got %d", len(norm.DomainBias))
	}
	if norm.DomainBias["example.com"] != 1 {
		t.Fatalf("expected example.com bias to clamp to 1, got %.2f", norm.DomainBias["example.com"])
	}
	if norm.DomainBias["bad"] != -1 {
		t.Fatalf("expected bad bias to clamp to -1, got %.2f", norm.DomainBias["bad"])
	}
	if norm.Alerts.LowCredibility != 0 {
		t.Fatalf("expected low credibility alert to clamp to 0, got %.2f", norm.Alerts.LowCredibility)
	}
	if norm.Alerts.HighBias != 1 {
		t.Fatalf("expected high bias alert to clamp to 1, got %.2f", norm.Alerts.HighBias)
	}
}

func TestFairnessValidate(t *testing.T) {
	cfg := FairnessConfig{BaselineCredibility: 0.6, MinCredibility: 0.2, BiasAdjustmentRange: 0.3}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	bad := FairnessConfig{BaselineCredibility: 0}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected validation error for baseline")
	}
}
