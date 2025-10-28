package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestMemoryHealthStats(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	now := time.Now().UTC()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM run_episodes`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM run_embeddings`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(12))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM plan_step_embeddings`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(novel_items),0), COALESCE(SUM(duplicate_items),0), MAX(created_at)
FROM memory_delta_events
WHERE created_at >= $1`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"novel", "duplicates", "last"}).AddRow(9, 3, now))

	stats, err := st.MemoryHealthStats(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("MemoryHealthStats: %v", err)
	}
	if stats.Episodes != 42 || stats.RunEmbeddings != 12 || stats.PlanEmbeddings != 7 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if stats.NovelDeltaCount != 9 || stats.DuplicateDeltaCnt != 3 {
		t.Fatalf("unexpected delta counts: %+v", stats)
	}
	if stats.LastDeltaAt == nil || !stats.LastDeltaAt.Equal(now) {
		t.Fatalf("expected LastDeltaAt to equal %v, got %+v", now, stats.LastDeltaAt)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMemoryHealthStatsIgnoreMissingTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	undefined := &pq.Error{Code: "42P01"}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM run_episodes`)).
		WillReturnError(undefined)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM run_embeddings`)).
		WillReturnError(undefined)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM plan_step_embeddings`)).
		WillReturnError(undefined)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(novel_items),0), COALESCE(SUM(duplicate_items),0), MAX(created_at)
FROM memory_delta_events
WHERE created_at >= $1`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnError(undefined)

	stats, err := st.MemoryHealthStats(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("MemoryHealthStats: %v", err)
	}
	if stats.Episodes != 0 || stats.RunEmbeddings != 0 || stats.PlanEmbeddings != 0 {
		t.Fatalf("expected zero counts, got %+v", stats)
	}
	if stats.NovelDeltaCount != 0 || stats.DuplicateDeltaCnt != 0 {
		t.Fatalf("expected zero delta counts, got %+v", stats)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
