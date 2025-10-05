package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestUpsertRunEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	rec := RunEmbeddingRecord{
		RunID:    "run-1",
		TopicID:  "topic-1",
		Kind:     "run_summary",
		Vector:   []float32{0.1, 0.2},
		Metadata: map[string]interface{}{"model": "text-embedding"},
	}

	query := regexp.QuoteMeta(`
INSERT INTO run_embeddings (run_id, topic_id, kind, embedding, metadata, created_at)
VALUES ($1,$2,$3,$4::vector,$5,NOW())
ON CONFLICT (run_id, kind) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  embedding = EXCLUDED.embedding,
  metadata = EXCLUDED.metadata,
  created_at = NOW();
`)
	mock.ExpectExec(query).
		WithArgs(rec.RunID, rec.TopicID, rec.Kind, "[0.1,0.2]", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.UpsertRunEmbedding(context.Background(), rec); err != nil {
		t.Fatalf("UpsertRunEmbedding: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReplacePlanStepEmbeddings(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	records := []PlanStepEmbeddingRecord{
		{
			RunID:   "run-1",
			TopicID: "topic-1",
			TaskID:  "task-1",
			Kind:    "analysis",
			Vector:  []float32{0.3, 0.4},
			Metadata: map[string]interface{}{
				"type": "analysis",
			},
		},
	}

	mock.ExpectBegin()

	deleteQuery := regexp.QuoteMeta(`DELETE FROM plan_step_embeddings WHERE run_id=$1`)
	mock.ExpectExec(deleteQuery).WithArgs("run-1").WillReturnResult(sqlmock.NewResult(0, 1))

	insertQuery := regexp.QuoteMeta(`
INSERT INTO plan_step_embeddings (run_id, topic_id, task_id, kind, embedding, metadata, created_at)
VALUES ($1,$2,$3,$4,$5::vector,$6,NOW())
ON CONFLICT (run_id, task_id, kind) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  embedding = EXCLUDED.embedding,
  metadata = EXCLUDED.metadata,
  created_at = NOW();
`)
	prep := mock.ExpectPrepare(insertQuery)
	prep.ExpectExec().
		WithArgs("run-1", "topic-1", "task-1", "analysis", "[0.3,0.4]", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	if err := st.ReplacePlanStepEmbeddings(context.Background(), "run-1", records); err != nil {
		t.Fatalf("ReplacePlanStepEmbeddings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSearchRunEmbeddings(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	query := regexp.QuoteMeta(`
SELECT run_id, topic_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM run_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`)
	now := time.Now()
	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "kind", "metadata", "created_at", "distance"}).
		AddRow("run-1", "topic-1", "run_summary", []byte(`{"score":0.9}`), now, 0.15)
	mock.ExpectQuery(query).
		WithArgs("[0.1,0.2]", "topic-1", 3).
		WillReturnRows(rows)

	results, err := st.SearchRunEmbeddings(context.Background(), "topic-1", []float32{0.1, 0.2}, 3, 0)
	if err != nil {
		t.Fatalf("SearchRunEmbeddings: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RunID != "run-1" || results[0].Distance != 0.15 {
		t.Fatalf("unexpected result: %+v", results[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSearchPlanStepEmbeddings(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	query := regexp.QuoteMeta(`
SELECT run_id, topic_id, task_id, kind, metadata, created_at, embedding <=> $1::vector AS distance
FROM plan_step_embeddings
WHERE ($2 = '' OR topic_id = $2)
ORDER BY embedding <=> $1::vector
LIMIT $3
`)
	now := time.Now()
	rows := sqlmock.NewRows([]string{"run_id", "topic_id", "task_id", "kind", "metadata", "created_at", "distance"}).
		AddRow("run-1", "topic-1", "task-1", "analysis", []byte(`{"weight":0.4}`), now, 0.25)
	mock.ExpectQuery(query).
		WithArgs("[0.5,0.6]", "topic-1", 4).
		WillReturnRows(rows)

	results, err := st.SearchPlanStepEmbeddings(context.Background(), "topic-1", []float32{0.5, 0.6}, 4, 0)
	if err != nil {
		t.Fatalf("SearchPlanStepEmbeddings: %v", err)
	}
	if len(results) != 1 || results[0].TaskID != "task-1" {
		t.Fatalf("unexpected results: %+v", results)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
