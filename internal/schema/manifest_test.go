package schema

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/internal/manifest"
)

func TestValidateRunManifest(t *testing.T) {
	payload := manifest.RunManifestPayload{
		Version:   manifest.RunManifestVersion,
		RunID:     "run-1",
		TopicID:   "topic-1",
		UserID:    "user-1",
		CreatedAt: time.Unix(0, 0).UTC(),
		Result: manifest.RunManifestResult{
			Summary:        "summary",
			DetailedReport: "details",
			Confidence:     0.8,
			CostEstimate:   12.5,
			TokensUsed:     100,
		},
		Sources: []manifest.ManifestSource{
			{ID: "s1", Title: "Source", URL: "https://example.com", Domain: "example.com"},
		},
	}
	signed, err := manifest.SignRunManifest(payload, "secret", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("SignRunManifest: %v", err)
	}
	if err := ValidateSignedRunManifest(signed); err != nil {
		t.Fatalf("ValidateSignedRunManifest: %v", err)
	}

	raw, _ := json.Marshal(signed)
	var modified map[string]interface{}
	_ = json.Unmarshal(raw, &modified)
	delete(modified, "checksum")
	bad, _ := json.Marshal(modified)
	if err := ValidateRunManifestDocument(bad); err == nil {
		t.Fatalf("expected validation error when checksum missing")
	}
}
