package store

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestPruneRunEmbeddingsBefore(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	cutoff := time.Now().Add(-24 * time.Hour)

	mock.ExpectExec(`DELETE FROM run_embeddings WHERE created_at < \$1`).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 5))

	deleted, err := st.PruneRunEmbeddingsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneRunEmbeddingsBefore returned error: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("expected 5 embeddings pruned, got %d", deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPrunePlanStepEmbeddingsBefore(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	cutoff := time.Now().Add(-48 * time.Hour)

	mock.ExpectExec(`DELETE FROM plan_step_embeddings WHERE created_at < \$1`).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 3))

	deleted, err := st.PrunePlanStepEmbeddingsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PrunePlanStepEmbeddingsBefore returned error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 embeddings pruned, got %d", deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPruneEmbeddingsBeforeWithZeroCutoff(t *testing.T) {
	st := &Store{}
	if _, err := st.PruneRunEmbeddingsBefore(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected error for zero cutoff in run embeddings prune")
	}
	if _, err := st.PrunePlanStepEmbeddingsBefore(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected error for zero cutoff in plan embeddings prune")
	}
}
