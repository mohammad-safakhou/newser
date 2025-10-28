package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/manifest"
)

// manifestSigningSecret resolves the secret used for run manifest signing.
func manifestSigningSecret(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	secret := strings.TrimSpace(cfg.Capability.SigningSecret)
	if secret == "" {
		return "", fmt.Errorf("capability.signing_secret not configured")
	}
	return secret, nil
}

// SignRunManifestPayload signs the supplied manifest payload using configuration defaults.
func SignRunManifestPayload(cfg *config.Config, payload manifest.RunManifestPayload, signedAt time.Time) (manifest.SignedRunManifest, error) {
	secret, err := manifestSigningSecret(cfg)
	if err != nil {
		return manifest.SignedRunManifest{}, err
	}
	return manifest.SignRunManifest(payload, secret, signedAt)
}

// VerifyRunManifestSignature validates checksum and signature for manifests using config defaults.
func VerifyRunManifestSignature(cfg *config.Config, signed manifest.SignedRunManifest) error {
	secret, err := manifestSigningSecret(cfg)
	if err != nil {
		return err
	}
	return manifest.VerifyRunManifest(signed, secret)
}
