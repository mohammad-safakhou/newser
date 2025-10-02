package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/mohammad-safakhou/newser/internal/policy"
)

func TestUpsertUpdatePolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	pol := policy.UpdatePolicy{
		RefreshInterval:    6 * time.Hour,
		DedupWindow:        24 * time.Hour,
		RepeatMode:         policy.RepeatModeAdaptive,
		FreshnessThreshold: 48 * time.Hour,
		Metadata: map[string]interface{}{
			"window": "weekday",
		},
	}

	query := regexp.QuoteMeta(`
INSERT INTO topic_update_policies (topic_id, refresh_interval_seconds, dedup_window_seconds, repeat_mode, freshness_threshold_seconds, metadata, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW())
ON CONFLICT (topic_id) DO UPDATE SET
  refresh_interval_seconds    = EXCLUDED.refresh_interval_seconds,
  dedup_window_seconds        = EXCLUDED.dedup_window_seconds,
  repeat_mode                 = EXCLUDED.repeat_mode,
  freshness_threshold_seconds = EXCLUDED.freshness_threshold_seconds,
  metadata                    = EXCLUDED.metadata,
  updated_at                  = NOW();
`)
	mock.ExpectExec(query).
		WithArgs("topic-123", int64(6*time.Hour/time.Second), int64(24*time.Hour/time.Second), string(policy.RepeatModeAdaptive), int64(48*time.Hour/time.Second), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.UpsertUpdatePolicy(context.Background(), "topic-123", pol); err != nil {
		t.Fatalf("UpsertUpdatePolicy: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetUpdatePolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	query := regexp.QuoteMeta(`
SELECT refresh_interval_seconds,
       dedup_window_seconds,
       repeat_mode,
       freshness_threshold_seconds,
       metadata
FROM topic_update_policies
WHERE topic_id=$1
`)
	mock.ExpectQuery(query).
		WithArgs("topic-123").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_interval_seconds", "dedup_window_seconds", "repeat_mode", "freshness_threshold_seconds", "metadata"}).
			AddRow(int64(3600), int64(7200), string(policy.RepeatModeAlways), int64(14400), []byte(`{"window":"weekend"}`)))

	pol, ok, err := st.GetUpdatePolicy(context.Background(), "topic-123")
	if err != nil {
		t.Fatalf("GetUpdatePolicy: %v", err)
	}
	if !ok {
		t.Fatalf("expected policy to exist")
	}
	if pol.RefreshInterval != time.Hour || pol.DedupWindow != 2*time.Hour || pol.FreshnessThreshold != 4*time.Hour {
		t.Fatalf("unexpected durations: %+v", pol)
	}
	if pol.RepeatMode != policy.RepeatModeAlways {
		t.Fatalf("unexpected repeat mode: %s", pol.RepeatMode)
	}
	if pol.Metadata["window"] != "weekend" {
		t.Fatalf("unexpected metadata: %+v", pol.Metadata)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetUpdatePolicyInvalidRepeatMode(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	query := regexp.QuoteMeta(`
SELECT refresh_interval_seconds,
       dedup_window_seconds,
       repeat_mode,
       freshness_threshold_seconds,
       metadata
FROM topic_update_policies
WHERE topic_id=$1
`)
	mock.ExpectQuery(query).
		WithArgs("topic-123").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_interval_seconds", "dedup_window_seconds", "repeat_mode", "freshness_threshold_seconds", "metadata"}).
			AddRow(int64(0), int64(0), "invalid", int64(0), []byte(`{}`)))

	if _, _, err := st.GetUpdatePolicy(context.Background(), "topic-123"); err == nil {
		t.Fatalf("expected validation error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
