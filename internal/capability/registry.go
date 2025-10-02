package capability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolCard represents registry metadata for a tool/agent.
type ToolCard struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	AgentType    string                 `json:"agent_type"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	OutputSchema map[string]interface{} `json:"output_schema"`
	CostEstimate float64                `json:"cost_estimate"`
	SideEffects  []string               `json:"side_effects"`
	Checksum     string                 `json:"checksum"`
	Signature    string                 `json:"signature"`
}

// DefaultToolCards returns built-in agent ToolCards with minimal schemas.
func DefaultToolCards() []ToolCard {
	empty := func() map[string]interface{} {
		return map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
		}
	}
	return []ToolCard{
		{Name: "research", Version: "v1", Description: "Collects sources", AgentType: "research", InputSchema: empty(), OutputSchema: empty(), SideEffects: []string{"network"}},
		{Name: "analysis", Version: "v1", Description: "Analyzes gathered sources", AgentType: "analysis", InputSchema: empty(), OutputSchema: empty()},
		{Name: "synthesis", Version: "v1", Description: "Synthesizes findings", AgentType: "synthesis", InputSchema: empty(), OutputSchema: empty()},
		{Name: "conflict_detection", Version: "v1", Description: "Detects conflicting statements", AgentType: "conflict_detection", InputSchema: empty(), OutputSchema: empty()},
		{Name: "highlight_management", Version: "v1", Description: "Extracts highlights", AgentType: "highlight_management", InputSchema: empty(), OutputSchema: empty()},
		{Name: "knowledge_graph", Version: "v1", Description: "Builds knowledge graph", AgentType: "knowledge_graph", InputSchema: empty(), OutputSchema: empty()},
	}
}

// Registry holds validated ToolCards keyed by agent type.
type Registry struct {
	tools map[string]ToolCard
}

// ErrToolMissing indicates a required tool is not registered.
var ErrToolMissing = fmt.Errorf("required tool missing")

// NewRegistry validates ToolCards and ensures required tools exist.
func NewRegistry(cards []ToolCard, signingSecret string, required []string) (*Registry, error) {
	reg := &Registry{tools: make(map[string]ToolCard)}
	for _, tc := range cards {
		if err := validateSignature(tc, signingSecret); err != nil {
			return nil, fmt.Errorf("tool %s@%s signature invalid: %w", tc.Name, tc.Version, err)
		}
		existing, ok := reg.tools[tc.AgentType]
		if !ok || versionGreater(tc.Version, existing.Version) {
			reg.tools[tc.AgentType] = tc
		}
	}
	if len(required) == 0 {
		required = []string{"research", "analysis", "synthesis", "conflict_detection", "highlight_management", "knowledge_graph"}
	}
	for _, r := range required {
		if _, ok := reg.tools[r]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrToolMissing, r)
		}
	}
	return reg, nil
}

// Tool returns the ToolCard for an agent type.
func (r *Registry) Tool(agentType string) (ToolCard, bool) {
	if r == nil {
		return ToolCard{}, false
	}
	tc, ok := r.tools[agentType]
	return tc, ok
}

// ComputeChecksum returns a deterministic hash of the ToolCard payload (excluding signature field).
func ComputeChecksum(tc ToolCard) (string, error) {
	payload := map[string]interface{}{
		"name":          tc.Name,
		"version":       tc.Version,
		"description":   tc.Description,
		"agent_type":    tc.AgentType,
		"input_schema":  tc.InputSchema,
		"output_schema": tc.OutputSchema,
		"cost_estimate": tc.CostEstimate,
		"side_effects":  tc.SideEffects,
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}

// SignToolCard computes an HMAC signature using the signing secret.
func SignToolCard(tc ToolCard, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("signing secret is empty")
	}
	checksum, err := ComputeChecksum(tc)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(checksum))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func validateSignature(tc ToolCard, secret string) error {
	if secret == "" {
		return nil
	}
	expected, err := SignToolCard(tc, secret)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(expected), []byte(tc.Signature)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func versionGreater(a, b string) bool {
	if a == b {
		return false
	}
	// naive semver compare
	return stringsCompare(splitVersion(a), splitVersion(b)) > 0
}

func splitVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		fmt.Sscanf(p, "%d", &out[i])
	}
	return out
}

func stringsCompare(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}
