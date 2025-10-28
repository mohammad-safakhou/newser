package policy

import (
	"testing"

	"github.com/mohammad-safakhou/newser/config"
)

func TestNewFairnessPolicy(t *testing.T) {
	cfg := config.FairnessConfig{}
	p, err := NewFairnessPolicy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Enabled {
		t.Fatalf("expected policy disabled by default")
	}

	enabled := config.FairnessConfig{
		Enabled:             true,
		BaselineCredibility: 0.6,
		MinCredibility:      0.4,
		BiasAdjustmentRange: 0.2,
		DomainBias:          map[string]float64{"example.com": 0.1},
	}
	p, err = NewFairnessPolicy(enabled)
	if err != nil {
		t.Fatalf("unexpected error creating enabled policy: %v", err)
	}
	if !p.Enabled {
		t.Fatalf("expected policy enabled")
	}
	bias := p.BiasFromPreferences(map[string]interface{}{"bias_slider": 0.5})
	if bias != 0.2 {
		t.Fatalf("expected bias to clamp to range, got %.2f", bias)
	}
	adj, delta := p.Adjust("https://example.com/news", 0.5, bias)
	if adj < 0.4 || adj > 1 {
		t.Fatalf("adjusted credibility out of range: %.2f", adj)
	}
	if delta == 0 {
		t.Fatalf("expected delta to reflect adjustment")
	}
}

func TestFairnessPolicyValidate(t *testing.T) {
	bad := config.FairnessConfig{Enabled: true, BaselineCredibility: 0}
	if _, err := NewFairnessPolicy(bad); err == nil {
		t.Fatalf("expected validation error for baseline")
	}
}
