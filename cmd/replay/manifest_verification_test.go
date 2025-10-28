package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/manifest"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

const manifestQuery = `SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at
FROM run_manifests
WHERE run_id=$1`

func TestVerifyRunManifestNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &store.Store{DB: db}
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "secret"}}

	mock.ExpectQuery(regexp.QuoteMeta(manifestQuery)).WithArgs("run-1").WillReturnError(sql.ErrNoRows)

	res, err := verifyRunManifest(context.Background(), st, cfg, "run-1")
	if err != nil {
		t.Fatalf("verifyRunManifest unexpected error: %v", err)
	}
	if res.Found {
		t.Fatalf("expected manifest to be absent")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVerifyRunManifestSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &store.Store{DB: db}
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "secret"}}

	signed := buildSignedManifest(t, cfg, "run-1")
	manifestBytes, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	createdAt := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow("run-1", "topic-1", manifestBytes, signed.Checksum, signed.Signature, signed.Algorithm, signed.SignedAt, createdAt)

	mock.ExpectQuery(regexp.QuoteMeta(manifestQuery)).WithArgs("run-1").WillReturnRows(rows)

	res, err := verifyRunManifest(context.Background(), st, cfg, "run-1")
	if err != nil {
		t.Fatalf("verifyRunManifest: %v", err)
	}
	if !res.Found {
		t.Fatalf("expected manifest to be present")
	}
	if res.Manifest.Checksum != signed.Checksum {
		t.Fatalf("checksum mismatch: got %s want %s", res.Manifest.Checksum, signed.Checksum)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVerifyRunManifestChecksumMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &store.Store{DB: db}
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "secret"}}

	signed := buildSignedManifest(t, cfg, "run-1")
	manifestBytes, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow("run-1", "topic-1", manifestBytes, signed.Checksum+"tampered", signed.Signature, signed.Algorithm, signed.SignedAt, time.Now().UTC())

	mock.ExpectQuery(regexp.QuoteMeta(manifestQuery)).WithArgs("run-1").WillReturnRows(rows)

	if _, err := verifyRunManifest(context.Background(), st, cfg, "run-1"); err == nil {
		t.Fatalf("expected checksum mismatch to be detected")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVerifyRunManifestSignatureValidation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &store.Store{DB: db}
	signerCfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "secret"}}
	signed := buildSignedManifest(t, signerCfg, "run-1")
	manifestBytes, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow("run-1", "topic-1", manifestBytes, signed.Checksum, signed.Signature, signed.Algorithm, signed.SignedAt, time.Now().UTC())

	mock.ExpectQuery(regexp.QuoteMeta(manifestQuery)).WithArgs("run-1").WillReturnRows(rows)

	// Use a different secret to force verification failure.
	verifierCfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "different"}}
	if _, err := verifyRunManifest(context.Background(), st, verifierCfg, "run-1"); err == nil {
		t.Fatalf("expected signature verification failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func buildSignedManifest(t *testing.T, cfg *config.Config, runID string) manifest.SignedRunManifest {
	t.Helper()
	payload := manifest.RunManifestPayload{
		Version:   manifest.RunManifestVersion,
		RunID:     runID,
		TopicID:   "topic-1",
		UserID:    "user-1",
		CreatedAt: time.Unix(1700, 0).UTC(),
	}
	signed, err := runtime.SignRunManifestPayload(cfg, payload, time.Unix(1700, 0))
	if err != nil {
		t.Fatalf("SignRunManifestPayload: %v", err)
	}
	return signed
}
