package core

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/capability"
)

type stubLLM struct{}

func (stubLLM) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) {
	return "", nil
}

func (stubLLM) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) {
	return "", 0, 0, nil
}

func (stubLLM) Embed(ctx context.Context, model string, input []string) ([][]float32, error) {
	return nil, nil
}

func (stubLLM) GetAvailableModels() []string { return []string{"stub"} }

func (stubLLM) GetModelInfo(model string) (ModelInfo, error) { return ModelInfo{Name: model}, nil }

func (stubLLM) CalculateCost(inputTokens, outputTokens int64, model string) float64 { return 0 }

func signAll(t *testing.T, cards []capability.ToolCard, secret string) []capability.ToolCard {
	t.Helper()
	result := make([]capability.ToolCard, len(cards))
	for i, tc := range cards {
		checksum, err := capability.ComputeChecksum(tc)
		if err != nil {
			t.Fatalf("checksum: %v", err)
		}
		tc.Checksum = checksum
		sig, err := capability.SignToolCard(tc, secret)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		tc.Signature = sig
		result[i] = tc
	}
	return result
}

func TestNewAgentsFailsWhenToolMissing(t *testing.T) {
	secret := "secret"
	defaults := capability.DefaultToolCards()
	minimal := []capability.ToolCard{defaults[0]} // research only
	signed := signAll(t, minimal, secret)
	reg, err := capability.NewRegistry(signed, secret, []string{"research"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	cfg := &config.Config{}
	tele := telemetry.NewTelemetry(config.TelemetryConfig{})

	if _, err := NewAgents(cfg, stubLLM{}, tele, reg); err == nil {
		t.Fatalf("expected error due to missing tool")
	}
}

func TestNewAgentsSucceedsWithCompleteRegistry(t *testing.T) {
	secret := "secret"
	signed := signAll(t, capability.DefaultToolCards(), secret)
	reg, err := capability.NewRegistry(signed, secret, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	cfg := &config.Config{}
	tele := telemetry.NewTelemetry(config.TelemetryConfig{})

	agents, err := NewAgents(cfg, stubLLM{}, tele, reg)
	if err != nil {
		t.Fatalf("NewAgents: %v", err)
	}
	expected := []string{"research", "analysis", "synthesis", "conflict_detection", "highlight_management", "knowledge_graph"}
	for _, name := range expected {
		if _, ok := agents[name]; !ok {
			t.Fatalf("expected agent %s to be present", name)
		}
	}
}
