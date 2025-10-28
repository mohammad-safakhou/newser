package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/manifest"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/schema"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type manifestVerificationResult struct {
	Found    bool
	Manifest manifest.SignedRunManifest
}

func verifyRunManifest(ctx context.Context, st *store.Store, cfg *config.Config, runID string) (manifestVerificationResult, error) {
	if st == nil {
		return manifestVerificationResult{}, fmt.Errorf("store is nil")
	}
	if cfg == nil {
		return manifestVerificationResult{}, fmt.Errorf("config is nil")
	}
	rec, ok, err := st.GetRunManifest(ctx, runID)
	if err != nil {
		return manifestVerificationResult{}, fmt.Errorf("get run manifest: %w", err)
	}
	if !ok {
		return manifestVerificationResult{Found: false}, nil
	}

	var signed manifest.SignedRunManifest
	if err := json.Unmarshal(rec.Manifest, &signed); err != nil {
		return manifestVerificationResult{}, fmt.Errorf("decode run manifest: %w", err)
	}

	if rec.Checksum != "" && rec.Checksum != signed.Checksum {
		return manifestVerificationResult{}, fmt.Errorf("run manifest checksum mismatch (stored=%s payload=%s)", rec.Checksum, signed.Checksum)
	}
	if rec.Signature != "" && rec.Signature != signed.Signature {
		return manifestVerificationResult{}, fmt.Errorf("run manifest signature mismatch")
	}
	if rec.Algorithm != "" && rec.Algorithm != signed.Algorithm {
		return manifestVerificationResult{}, fmt.Errorf("run manifest algorithm mismatch (stored=%s payload=%s)", rec.Algorithm, signed.Algorithm)
	}
	if !rec.SignedAt.IsZero() && !rec.SignedAt.UTC().Equal(signed.SignedAt.UTC()) {
		return manifestVerificationResult{}, fmt.Errorf("run manifest signed_at mismatch (stored=%s payload=%s)", rec.SignedAt.UTC().Format(time.RFC3339), signed.SignedAt.UTC().Format(time.RFC3339))
	}

	if err := schema.ValidateSignedRunManifest(signed); err != nil {
		return manifestVerificationResult{}, fmt.Errorf("run manifest schema invalid: %w", err)
	}
	if err := runtime.VerifyRunManifestSignature(cfg, signed); err != nil {
		return manifestVerificationResult{}, fmt.Errorf("run manifest signature verification failed: %w", err)
	}

	return manifestVerificationResult{Found: true, Manifest: signed}, nil
}

func logManifestStatus(result manifestVerificationResult, runID string) {
	if !result.Found {
		fmt.Fprintf(os.Stderr, "Run manifest not found for run %s\n", runID)
		return
	}
	fmt.Fprintf(os.Stderr, "Run manifest verified for run %s (algorithm=%s signed_at=%s)\n",
		runID,
		result.Manifest.Algorithm,
		result.Manifest.SignedAt.UTC().Format(time.RFC3339),
	)
}
