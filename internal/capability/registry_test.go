package capability

import "testing"

func mustSign(t *testing.T, tc ToolCard, secret string) ToolCard {
	t.Helper()
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
		Name:        "analysis",
		Version:     "v1",
		Description: "analysis tool",
		AgentType:   "analysis",
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
