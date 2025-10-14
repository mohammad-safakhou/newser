package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/mohammad-safakhou/newser/internal/budget"
)

func TestUpsertTopicBudgetConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	maxCost := 12.5
	maxTokens := int64(50000)
	cfg := budget.Config{
		MaxCost:   &maxCost,
		MaxTokens: &maxTokens,
		Metadata:  map[string]interface{}{"team": "intel"},
	}

	query := regexp.QuoteMeta(`
INSERT INTO topic_budget_configs (topic_id, max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,NOW(),NOW())
ON CONFLICT (topic_id) DO UPDATE SET
  max_cost = EXCLUDED.max_cost,
  max_tokens = EXCLUDED.max_tokens,
  max_time_seconds = EXCLUDED.max_time_seconds,
  approval_threshold = EXCLUDED.approval_threshold,
  require_approval = EXCLUDED.require_approval,
  metadata = EXCLUDED.metadata,
  updated_at = NOW();
`)
	mock.ExpectExec(query).
		WithArgs("topic-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), false, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.UpsertTopicBudgetConfig(context.Background(), "topic-1", cfg); err != nil {
		t.Fatalf("UpsertTopicBudgetConfig: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetTopicBudgetConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	query := regexp.QuoteMeta(`
SELECT max_cost, max_tokens, max_time_seconds, approval_threshold, require_approval, metadata
FROM topic_budget_configs
WHERE topic_id=$1
`)
	mock.ExpectQuery(query).
		WithArgs("topic-1").
		WillReturnRows(sqlmock.NewRows([]string{"max_cost", "max_tokens", "max_time_seconds", "approval_threshold", "require_approval", "metadata"}).
			AddRow(15.75, int64(80000), int64(3600), nil, true, []byte(`{"team":"intel"}`)))

	cfg, ok, err := st.GetTopicBudgetConfig(context.Background(), "topic-1")
	if err != nil {
		t.Fatalf("GetTopicBudgetConfig: %v", err)
	}
	if !ok {
		t.Fatalf("expected budget config")
	}
	if cfg.MaxCost == nil || *cfg.MaxCost != 15.75 {
		t.Fatalf("unexpected max cost: %#v", cfg.MaxCost)
	}
	if !cfg.RequireApproval {
		t.Fatalf("expected require approval")
	}
	if cfg.Metadata["team"].(string) != "intel" {
		t.Fatalf("unexpected metadata: %#v", cfg.Metadata)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestApplyRunBudget(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	maxTokens := int64(40000)
	cfg := budget.Config{RequireApproval: true, MaxTokens: &maxTokens}

	query := regexp.QuoteMeta(`
UPDATE runs SET
  budget_cost_limit = $2,
  budget_token_limit = $3,
  budget_time_seconds = $4,
  budget_approval_threshold = $5,
  budget_require_approval = $6
WHERE id = $1
`)
	mock.ExpectExec(query).
		WithArgs("run-abc", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.ApplyRunBudget(context.Background(), "run-abc", cfg); err != nil {
		t.Fatalf("ApplyRunBudget: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBudgetApprovalLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	mock.ExpectExec(`INSERT INTO run_budget_approvals`).
		WithArgs("run-1", "topic-1", 15.0, 10.0, "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.CreateBudgetApproval(context.Background(), "run-1", "topic-1", "user-1", 15.0, 10.0); err != nil {
		t.Fatalf("CreateBudgetApproval: %v", err)
	}

	mock.ExpectQuery(`SELECT run_id, topic_id, estimated_cost`).
		WithArgs("topic-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "topic_id", "estimated_cost", "approval_threshold", "requested_by", "status", "created_at", "decided_at", "decided_by", "reason"}).
			AddRow("run-1", "topic-1", 15.0, 10.0, "user-1", "pending", time.Now(), nil, nil, ""))

	if _, ok, err := st.GetPendingBudgetApproval(context.Background(), "topic-1"); err != nil || !ok {
		t.Fatalf("GetPendingBudgetApproval failed: %v ok=%v", err, ok)
	}

	mock.ExpectExec(`UPDATE run_budget_approvals SET status=`).
		WithArgs("run-1", "approved", "approver", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := st.ResolveBudgetApproval(context.Background(), "run-1", true, "approver", nil); err != nil {
		t.Fatalf("ResolveBudgetApproval: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRecordBudgetBreach(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	maxCost := 10.0
	cfg := budget.Config{MaxCost: &maxCost, RequireApproval: true}
	usage := budget.Usage{Cost: 12.5}
	exceeded := budget.ErrExceeded{Kind: "cost", Usage: "$12.50", Limit: "$10.00"}

	mock.ExpectExec(`INSERT INTO run_budget_events`).
		WithArgs("run-1", "topic-1", "breach", sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := st.RecordBudgetBreach(context.Background(), "run-1", "topic-1", cfg, usage, exceeded); err != nil {
		t.Fatalf("RecordBudgetBreach: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRecordBudgetOverride(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}
	reason := "Approved after manual review"

	mock.ExpectExec(`INSERT INTO run_budget_events`).
		WithArgs("run-1", "topic-1", "override.approved", sqlmock.AnyArg(), "user-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := st.RecordBudgetOverride(context.Background(), "run-1", "topic-1", "user-1", true, &reason); err != nil {
		t.Fatalf("RecordBudgetOverride: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
