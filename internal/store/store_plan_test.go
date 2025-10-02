package store

import (
    "context"
    "regexp"
    "testing"
    "time"

    sqlmock "github.com/DATA-DOG/go-sqlmock"
    "github.com/lib/pq"
)

func TestSavePlanGraph(t *testing.T) {
    db, mock, err := sqlmock.New()
    if err != nil {
        t.Fatalf("sqlmock.New: %v", err)
    }
    defer db.Close()

    st := &Store{DB: db}
    rec := PlanGraphRecord{
        PlanID:        "plan-1",
        ThoughtID:     "thought-1",
        Version:       "v1",
        Confidence:    0.9,
        ExecutionOrder: []string{"t1"},
        Budget:        []byte(`{"max_cost":5}`),
        Estimates:     []byte(`{"total_cost":3}`),
        PlanJSON:      []byte(`{"version":"v1","tasks":[{"id":"t1","type":"research"}]}`),
    }

    query := regexp.QuoteMeta(`
INSERT INTO plan_graphs (plan_id, thought_id, version, confidence, execution_order, budget, estimates, plan_json, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW(),NOW())
ON CONFLICT (plan_id) DO UPDATE SET
  thought_id = EXCLUDED.thought_id,
  version = EXCLUDED.version,
  confidence = EXCLUDED.confidence,
  execution_order = EXCLUDED.execution_order,
  budget = EXCLUDED.budget,
  estimates = EXCLUDED.estimates,
  plan_json = EXCLUDED.plan_json,
  updated_at = NOW();
`)
    mock.ExpectExec(query).
        WithArgs(rec.PlanID, rec.ThoughtID, rec.Version, rec.Confidence, sqlmock.AnyArg(), rec.Budget, rec.Estimates, rec.PlanJSON).
        WillReturnResult(sqlmock.NewResult(0, 1))

    if err := st.SavePlanGraph(context.Background(), rec); err != nil {
        t.Fatalf("SavePlanGraph: %v", err)
    }

    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations: %v", err)
    }
}

func TestGetLatestPlanGraph(t *testing.T) {
    db, mock, err := sqlmock.New()
    if err != nil {
        t.Fatalf("sqlmock.New: %v", err)
    }
    defer db.Close()

    st := &Store{DB: db}

    now := time.Now()
    query := regexp.QuoteMeta(`
SELECT plan_id, thought_id, version, confidence, execution_order, budget, estimates, plan_json, created_at, updated_at
FROM plan_graphs
WHERE thought_id=$1
ORDER BY updated_at DESC
LIMIT 1
`)
    mock.ExpectQuery(query).
        WithArgs("thought-1").
        WillReturnRows(sqlmock.NewRows([]string{"plan_id", "thought_id", "version", "confidence", "execution_order", "budget", "estimates", "plan_json", "created_at", "updated_at"}).
            AddRow("plan-1", "thought-1", "v1", 0.8, pq.StringArray{"t1"}, []byte(`{"max_cost":5}`), []byte(`{"total_cost":3}`), []byte(`{"version":"v1"}`), now, now))

    rec, ok, err := st.GetLatestPlanGraph(context.Background(), "thought-1")
    if err != nil {
        t.Fatalf("GetLatestPlanGraph: %v", err)
    }
    if !ok {
        t.Fatalf("expected record")
    }
    if rec.PlanID != "plan-1" || len(rec.ExecutionOrder) != 1 || rec.ExecutionOrder[0] != "t1" {
        t.Fatalf("unexpected record: %+v", rec)
    }

    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations: %v", err)
    }
}
