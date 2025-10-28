package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestUpsertProceduralTemplateFingerprint(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	sampleGraph := []byte(`{"stage":"stage-01","tasks":["t1","t2"]}`)
	sampleParams := []byte(`{"task_count":2}`)
	metaJSON := []byte(`{"stage":"stage-01"}`)
	now := time.Now()

	query := regexp.QuoteMeta(`
INSERT INTO procedural_template_fingerprints (topic_id, fingerprint, occurrences, last_seen_at, sample_graph, sample_parameters, metadata)
VALUES ($1,$2,1,NOW(),$3,$4,$5)
ON CONFLICT (topic_id, fingerprint) DO UPDATE SET
  occurrences = procedural_template_fingerprints.occurrences + 1,
  last_seen_at = NOW(),
  sample_graph = CASE WHEN procedural_template_fingerprints.template_id IS NULL THEN EXCLUDED.sample_graph ELSE procedural_template_fingerprints.sample_graph END,
  sample_parameters = CASE WHEN procedural_template_fingerprints.template_id IS NULL THEN EXCLUDED.sample_parameters ELSE procedural_template_fingerprints.sample_parameters END,
  metadata = jsonb_strip_nulls(procedural_template_fingerprints.metadata || EXCLUDED.metadata),
  updated_at = NOW()
RETURNING topic_id, fingerprint, occurrences, last_seen_at, template_id, sample_graph, sample_parameters, metadata, created_at, updated_at
`)
	mock.ExpectQuery(query).
		WithArgs("topic-1", "fingerprint-1", sampleGraph, sampleParams, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"topic_id", "fingerprint", "occurrences", "last_seen_at", "template_id", "sample_graph", "sample_parameters", "metadata", "created_at", "updated_at",
		}).AddRow("topic-1", "fingerprint-1", 3, now, "template-1", sampleGraph, sampleParams, metaJSON, now.Add(-time.Hour), now))

	rec, err := st.UpsertProceduralTemplateFingerprint(context.Background(), ProceduralTemplateFingerprintRecord{
		TopicID:      "topic-1",
		Fingerprint:  "fingerprint-1",
		SampleGraph:  sampleGraph,
		SampleParams: sampleParams,
		Metadata:     map[string]interface{}{"stage": "stage-01"},
	})
	if err != nil {
		t.Fatalf("UpsertProceduralTemplateFingerprint: %v", err)
	}
	if rec.Occurrences != 3 {
		t.Fatalf("expected occurrences=3, got %d", rec.Occurrences)
	}
	if rec.TemplateID != "template-1" {
		t.Fatalf("expected template id template-1, got %q", rec.TemplateID)
	}
	if rec.Metadata["stage"] != "stage-01" {
		t.Fatalf("expected metadata stage, got %#v", rec.Metadata)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRecordProceduralTemplateUsageWithMetrics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	now := time.Now()

	insertUsage := regexp.QuoteMeta(`
INSERT INTO procedural_template_usage (run_id, topic_id, template_id, template_version, fingerprint, stage, success, latency_ms, metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING id, run_id, topic_id, template_id, template_version, fingerprint, stage, success, latency_ms, metadata, created_at
`)
	mock.ExpectQuery(insertUsage).
		WithArgs("run-1", "topic-1", "template-1", 2, "fingerprint-1", "stage-01", true, 123.0, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "run_id", "topic_id", "template_id", "template_version", "fingerprint", "stage", "success", "latency_ms", "metadata", "created_at",
		}).AddRow(int64(42), "run-1", "topic-1", "template-1", 2, "fingerprint-1", "stage-01", true, 123.0, []byte(`{"stage":"stage-01"}`), now))

	updateMetrics := regexp.QuoteMeta(`
INSERT INTO procedural_template_metrics (template_id, usage_count, success_count, total_latency_ms, last_used_at, updated_at)
VALUES ($1,1,$2,$3,NOW(),NOW())
ON CONFLICT (template_id) DO UPDATE SET
  usage_count = procedural_template_metrics.usage_count + 1,
  success_count = procedural_template_metrics.success_count + $2,
  total_latency_ms = procedural_template_metrics.total_latency_ms + $3,
  last_used_at = NOW(),
  updated_at = NOW()
`)
	mock.ExpectExec(updateMetrics).
		WithArgs("template-1", 1, 123.0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	record, err := st.RecordProceduralTemplateUsage(context.Background(), ProceduralTemplateUsageRecord{
		RunID:           "run-1",
		TopicID:         "topic-1",
		TemplateID:      "template-1",
		TemplateVersion: 2,
		Fingerprint:     "fingerprint-1",
		Stage:           "stage-01",
		Success:         true,
		LatencyMS:       123.0,
		Metadata:        map[string]interface{}{"stage": "stage-01"},
	})
	if err != nil {
		t.Fatalf("RecordProceduralTemplateUsage: %v", err)
	}
	if record.ID != 42 {
		t.Fatalf("expected usage id 42, got %d", record.ID)
	}
	if record.TemplateID != "template-1" {
		t.Fatalf("expected template id template-1, got %q", record.TemplateID)
	}
	if record.Metadata["stage"] != "stage-01" {
		t.Fatalf("expected metadata stage-01, got %#v", record.Metadata)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
