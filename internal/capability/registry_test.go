package capability

import "testing"

func minimalSchema() map[string]interface{} {
	return map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
	}
}

func mustSign(t *testing.T, tc ToolCard, secret string) ToolCard {
	t.Helper()
	if tc.InputSchema == nil {
		tc.InputSchema = minimalSchema()
	}
	if tc.OutputSchema == nil {
		tc.OutputSchema = minimalSchema()
	}
	checksum, err := ComputeChecksum(tc)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	tc.Checksum = checksum
	sig, err := SignToolCard(tc, secret)
	if err != nil {
		t.Fatalf("SignToolCard: %v", err)
	}
	tc.Signature = sig
	return tc
}

func TestNewRegistryRejectsInvalidSignature(t *testing.T) {
	secret := "top-secret"
	tc := ToolCard{
		Name:         "analysis",
		Version:      "v1",
		Description:  "analysis tool",
		AgentType:    "analysis",
		InputSchema:  minimalSchema(),
		OutputSchema: minimalSchema(),
	}
	checksum, err := ComputeChecksum(tc)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	tc.Checksum = checksum
	tc.Signature = "deadbeef"

	if _, err := NewRegistry([]ToolCard{tc}, secret, []string{"analysis"}); err == nil {
		t.Fatalf("expected signature validation to fail")
	}
}

func TestNewRegistryEnforcesRequiredTools(t *testing.T) {
	secret := "top-secret"
	research := mustSign(t, ToolCard{
		Name:        "research",
		Version:     "v1",
		AgentType:   "research",
		Description: "research tool",
	}, secret)

	cards := []ToolCard{research}
	if _, err := NewRegistry(cards, secret, []string{"research", "analysis"}); err == nil {
		t.Fatalf("expected missing required tool to error")
	}
}

func TestNewRegistryPrefersLatestVersionPerAgent(t *testing.T) {
	secret := "top-secret"
	old := mustSign(t, ToolCard{
		Name:      "research",
		Version:   "v1",
		AgentType: "research",
	}, secret)
	newer := mustSign(t, ToolCard{
		Name:      "research",
		Version:   "v1.1",
		AgentType: "research",
	}, secret)

	reg, err := NewRegistry([]ToolCard{old, newer}, secret, []string{"research"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	tool, ok := reg.Tool("research")
	if !ok {
		t.Fatalf("expected research tool to exist")
	}
	if tool.Version != "v1.1" {
		t.Fatalf("expected latest version, got %s", tool.Version)
	}
}

func TestValidateToolCard(t *testing.T) {
	valid := ToolCard{
		Name:         "analysis",
		Version:      "v1",
		AgentType:    "analysis",
		Description:  "analysis tool",
		InputSchema:  minimalSchema(),
		OutputSchema: minimalSchema(),
		CostEstimate: 0.5,
	}
	if err := ValidateToolCard(valid); err != nil {
		t.Fatalf("expected valid tool card, got %v", err)
	}
	invalid := ToolCard{
		Name:         "",
		Version:      "v1",
		AgentType:    "analysis",
		InputSchema:  minimalSchema(),
		OutputSchema: minimalSchema(),
	}
	if err := ValidateToolCard(invalid); err == nil {
		t.Fatalf("expected validation failure for missing name")
	}
	badSchema := ToolCard{
		Name:         "analysis",
		Version:      "v1",
		AgentType:    "analysis",
		InputSchema:  map[string]interface{}{"type": 123},
		OutputSchema: minimalSchema(),
	}
	if err := ValidateToolCard(badSchema); err == nil {
		t.Fatalf("expected validation failure for invalid schema")
	}
}

func TestVerifyChecksum(t *testing.T) {
	card := ToolCard{
		Name:         "analysis",
		Version:      "v1",
		AgentType:    "analysis",
		InputSchema:  minimalSchema(),
		OutputSchema: minimalSchema(),
	}
	checksum, err := ComputeChecksum(card)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	card.Checksum = checksum
	if err := VerifyChecksum(card); err != nil {
		t.Fatalf("expected checksum to validate, got %v", err)
	}
	card.Checksum = "deadbeef"
	if err := VerifyChecksum(card); err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
}
