package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestGetAttachment(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	now := time.Now()

	query := regexp.QuoteMeta(`
SELECT id, run_id, task_id, attempt, artifact_id,
       COALESCE(name,''), COALESCE(media_type,''), COALESCE(size_bytes,0),
       COALESCE(checksum,''), storage_uri, retention_expires_at, metadata, created_at, updated_at
FROM attachments
WHERE id=$1
`)
	mock.ExpectQuery(query).
		WithArgs("att-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "run_id", "task_id", "attempt", "artifact_id",
			"name", "media_type", "size_bytes", "checksum",
			"storage_uri", "retention_expires_at", "metadata", "created_at", "updated_at",
		}).AddRow(
			"att-1", "run-1", "task-1", 0, "artifact",
			"Report", "text/plain", int64(128), "cs",
			"s3://bucket/object", now, []byte(`{"k":"v"}`), now, now,
		))

	rec, ok, err := st.GetAttachment(context.Background(), "att-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if !ok {
		t.Fatalf("expected attachment to exist")
	}
	if rec.ID != "att-1" || rec.RunID != "run-1" {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if rec.RetentionExpiresAt == nil || !rec.RetentionExpiresAt.Equal(now) {
		t.Fatalf("expected retention timestamp to be set")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestUpdateAttachmentRetention(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	st := &Store{DB: db}

	expires := time.Now().Add(24 * time.Hour)
	query := regexp.QuoteMeta(`
UPDATE attachments
SET retention_expires_at=$2,
    updated_at=NOW()
WHERE id=$1
`)
	mock.ExpectExec(query).
		WithArgs("att-1", expires.UTC()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.UpdateAttachmentRetention(context.Background(), "att-1", &expires); err != nil {
		t.Fatalf("UpdateAttachmentRetention: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPurgeExpiredAttachments(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	st := &Store{DB: db}

	before := time.Now()
	query := regexp.QuoteMeta(`
DELETE FROM attachments
WHERE id IN (
    SELECT id FROM attachments
    WHERE retention_expires_at IS NOT NULL
      AND retention_expires_at <= $1
    ORDER BY retention_expires_at ASC
    LIMIT $2
)
`)
	mock.ExpectExec(query).
		WithArgs(before, 50).
		WillReturnResult(sqlmock.NewResult(0, 3))

	count, err := st.PurgeExpiredAttachments(context.Background(), before, 50)
	if err != nil {
		t.Fatalf("PurgeExpiredAttachments: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected to purge 3 rows, got %d", count)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
