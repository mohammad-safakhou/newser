package runtime

import (
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/manifest"
)

func TestSignRunManifestPayload(t *testing.T) {
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "secret"}}
	payload := manifest.RunManifestPayload{RunID: "run-1", TopicID: "topic-1", UserID: "user-1", Version: manifest.RunManifestVersion, CreatedAt: time.Unix(0, 0)}

	signed, err := SignRunManifestPayload(cfg, payload, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("SignRunManifestPayload: %v", err)
	}
	if err := VerifyRunManifestSignature(cfg, signed); err != nil {
		t.Fatalf("VerifyRunManifestSignature: %v", err)
	}
}

func TestSignRunManifestPayloadMissingSecret(t *testing.T) {
	cfg := &config.Config{}
	_, err := SignRunManifestPayload(cfg, manifest.RunManifestPayload{}, time.Now())
	if err == nil {
		t.Fatalf("expected error when secret missing")
	}
}
