package policy

import (
	"testing"

	"github.com/mohammad-safakhou/newser/config"
)

func TestNewExplainabilityPolicyDefaults(t *testing.T) {
	policy, err := NewExplainabilityPolicy(config.ExplainabilityConfig{})
	if err != nil {
		t.Fatalf("NewExplainabilityPolicy: %v", err)
	}
	if policy.MaxTraceDepth != 50 {
		t.Fatalf("expected default trace depth 50, got %d", policy.MaxTraceDepth)
	}
	if policy.EvidenceGraph || policy.HoverContext || policy.IncludeMemory || policy.ShowPlannerTrace {
		t.Fatalf("expected toggles to default false: %+v", policy)
	}
}

func TestNewExplainabilityPolicyValidation(t *testing.T) {
	_, err := NewExplainabilityPolicy(config.ExplainabilityConfig{MaxTraceDepth: -5})
	if err == nil {
		t.Fatalf("expected error for negative trace depth")
	}
}
