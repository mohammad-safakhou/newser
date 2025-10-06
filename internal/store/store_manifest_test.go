package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestInsertRunManifest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	payload := json.RawMessage(`{"manifest":{}}`)
	rec := RunManifestRecord{
		RunID:     "run-1",
		TopicID:   "topic-1",
		Manifest:  payload,
		Checksum:  "abc",
		Signature: "sig",
		Algorithm: "hmac-sha256",
		SignedAt:  time.Unix(0, 0),
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO run_manifests (run_id, topic_id, manifest, checksum, signature, algorithm, signed_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING")).
		WithArgs(rec.RunID, rec.TopicID, payload, rec.Checksum, rec.Signature, rec.Algorithm, rec.SignedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := st.InsertRunManifest(context.Background(), rec); err != nil {
		t.Fatalf("InsertRunManifest: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestInsertRunManifestDedup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	payload := json.RawMessage(`{"manifest":{}}`)
	rec := RunManifestRecord{RunID: "run-1", TopicID: "topic-1", Manifest: payload, Checksum: "abc", Signature: "sig", Algorithm: "hmac-sha256", SignedAt: time.Unix(0, 0)}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO run_manifests (run_id, topic_id, manifest, checksum, signature, algorithm, signed_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING")).
		WithArgs(rec.RunID, rec.TopicID, payload, rec.Checksum, rec.Signature, rec.Algorithm, rec.SignedAt).
		WillReturnResult(sqlmock.NewResult(1, 0))

	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow(rec.RunID, rec.TopicID, []byte(payload), rec.Checksum, rec.Signature, rec.Algorithm, rec.SignedAt, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at FROM run_manifests WHERE run_id=$1")).
		WithArgs(rec.RunID).
		WillReturnRows(rows)

	if err := st.InsertRunManifest(context.Background(), rec); err != nil {
		t.Fatalf("InsertRunManifest dedup: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestInsertRunManifestConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	payload := json.RawMessage(`{"manifest":{}}`)
	rec := RunManifestRecord{RunID: "run-1", TopicID: "topic-1", Manifest: payload, Checksum: "abc", Signature: "sig", Algorithm: "hmac", SignedAt: time.Unix(0, 0)}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO run_manifests (run_id, topic_id, manifest, checksum, signature, algorithm, signed_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING")).
		WithArgs(rec.RunID, rec.TopicID, payload, rec.Checksum, rec.Signature, rec.Algorithm, rec.SignedAt).
		WillReturnResult(sqlmock.NewResult(1, 0))

	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "manifest", "checksum", "signature", "algorithm", "signed_at", "created_at"}).
		AddRow(rec.RunID, rec.TopicID, []byte(`{"manifest":{"version":"v1"}}`), "different", "other", "hmac", rec.SignedAt, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at FROM run_manifests WHERE run_id=$1")).
		WithArgs(rec.RunID).
		WillReturnRows(rows)

	err = st.InsertRunManifest(context.Background(), rec)
	if !errors.Is(err, ErrRunManifestExists) {
		t.Fatalf("expected ErrRunManifestExists, got %v", err)
	}
}

func TestGetRunManifestNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, topic_id, manifest, checksum, signature, algorithm, signed_at, created_at FROM run_manifests WHERE run_id=$1")).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}))

	_, ok, err := st.GetRunManifest(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetRunManifest: %v", err)
	}
	if ok {
		t.Fatalf("expected not found")
	}
}
