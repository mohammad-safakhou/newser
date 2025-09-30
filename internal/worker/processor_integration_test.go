package worker_test

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/schema"
	"github.com/mohammad-safakhou/newser/internal/store"
	"github.com/mohammad-safakhou/newser/internal/worker"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcPostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcRedis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	otelnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

func TestWorkerResumeFromCheckpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	pgUser := "newser"
	pgPassword := "newser"
	pgDB := "newser"

	pgC, err := tcPostgres.RunContainer(ctx,
		tcPostgres.WithDatabase(pgDB),
		tcPostgres.WithUsername(pgUser),
		tcPostgres.WithPassword(pgPassword),
		tcPostgres.WithInitScripts(),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("postgres container: %v", err)
	}
	defer func() {
		_ = pgC.Terminate(ctx)
	}()

	pgHost, err := pgC.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	pgPort, err := pgC.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("postgres port: %v", err)
	}

	redisC, err := tcRedis.RunContainer(ctx, testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp")))
	if err != nil {
		t.Fatalf("redis container: %v", err)
	}
	defer func() { _ = redisC.Terminate(ctx) }()

	redisHost, err := redisC.Host(ctx)
	if err != nil {
		t.Fatalf("redis host: %v", err)
	}
	redisPort, err := redisC.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("redis port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", pgUser, pgPassword, pgHost, pgPort.Port(), pgDB)
	if err := applyMigrations(ctx, dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}

	if err := seedTopic(ctx, st.DB); err != nil {
		t.Fatalf("seed topic: %v", err)
	}

	if err := schema.SeedBaseSchemas(ctx, st); err != nil {
		t.Fatalf("seed schemas: %v", err)
	}
	registry, err := schema.Load(ctx, st)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", redisHost, redisPort.Port())})
	defer func() { _ = redisClient.Close() }()

	if err := streams.EnsureGroup(ctx, redisClient, worker.StreamRunEnqueued, "test-group"); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	publisher := streams.NewPublisher(redisClient, registry)
	runPayload := map[string]interface{}{
		"topic_id":             seededTopicID,
		"user_id":              seededUserID,
		"trigger":              "manual",
		"preferences_snapshot": map[string]interface{}{"focus": "integration"},
		"context_snapshot":     map[string]interface{}{"known_urls": []string{"https://example.com"}},
	}
	if _, err := publisher.PublishRaw(ctx, worker.StreamRunEnqueued, worker.StreamRunEnqueued, "v1", runPayload); err != nil {
		t.Fatalf("publish run: %v", err)
	}

	consumeRedis := func(name string) *streams.Consumer {
		return streams.NewConsumer(redisClient, registry, "test-group", name)
	}

	// First processor run (simulate crash after dispatch).
	consumer1 := consumeRedis("consumer-1")
	noopMeter := otelnoop.NewMeterProvider().Meter("worker-test")
	noopTracer := trace.NewNoopTracerProvider().Tracer("worker-test")
	proc := worker.NewProcessor(log.New(os.Stdout, "[TEST] ", log.LstdFlags), st, publisher, consumer1, worker.StreamRunEnqueued, worker.StreamTaskDispatch, noopMeter, noopTracer)

	ctx1, cancel1 := context.WithCancel(ctx)
	done1 := make(chan error, 1)
	go func() {
		done1 <- proc.Start(ctx1)
	}()

	awaitCheckpoint(t, ctx, st, store.CheckpointStatusDispatched, 10*time.Second)
	dispatched, err := st.ListCheckpointsByStatus(ctx, store.CheckpointStatusDispatched)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	if len(dispatched) == 0 {
		t.Fatalf("expected dispatched checkpoint")
	}
	runID := dispatched[0].RunID

	cancel1()
	if err := <-done1; err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("first processor exit: %v", err)
	}

	cpBefore, ok, err := st.GetCheckpoint(ctx, runID, "bootstrap.dispatch")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if !ok {
		t.Fatalf("checkpoint not found for run %s", runID)
	}
	if cpBefore.Retries != 0 {
		t.Fatalf("expected retries 0, got %d", cpBefore.Retries)
	}

	lenBefore, err := redisClient.XLen(ctx, worker.StreamTaskDispatch).Result()
	if err != nil {
		t.Fatalf("xlen before: %v", err)
	}

	// Restart processor to trigger resume.
	consumer2 := consumeRedis("consumer-2")
	proc2 := worker.NewProcessor(log.New(os.Stdout, "[TEST] ", log.LstdFlags), st, publisher, consumer2, worker.StreamRunEnqueued, worker.StreamTaskDispatch, noopMeter, noopTracer)
	ctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	done2 := make(chan error, 1)
	go func() {
		done2 <- proc2.Start(ctx2)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel2()
	if err := <-done2; err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("second processor exit: %v", err)
	}

	lenAfter, err := redisClient.XLen(ctx, worker.StreamTaskDispatch).Result()
	if err != nil {
		t.Fatalf("xlen after: %v", err)
	}
	if lenAfter <= lenBefore {
		t.Fatalf("expected task stream length to increase, before=%d after=%d", lenBefore, lenAfter)
	}

	checkpoints, err := st.ListCheckpointsByStatus(ctx, store.CheckpointStatusDispatched)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	var found bool
	for _, cp := range checkpoints {
		if cp.RunID == runID && cp.Stage == "bootstrap.dispatch" {
			if cp.Retries < 1 {
				t.Fatalf("expected checkpoint retries incremented, got %d", cp.Retries)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected checkpoint for run %s", runID)
	}
}

var (
	seededUserID  = uuid.New().String()
	seededTopicID = uuid.New().String()
)

func applyMigrations(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "SET search_path TO public"); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	schemaSQL := `
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS topics (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  preferences JSONB NOT NULL DEFAULT '{}'::jsonb,
  schedule_cron TEXT NOT NULL DEFAULT '@daily',
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  started_at TIMESTAMPTZ DEFAULT NOW(),
  finished_at TIMESTAMPTZ,
  error TEXT
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (scope, key)
);

CREATE TABLE IF NOT EXISTS queue_checkpoints (
  run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  stage TEXT NOT NULL,
  checkpoint_token TEXT NOT NULL,
  status TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  retries INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (run_id, stage)
);

CREATE TABLE IF NOT EXISTS message_schemas (
  event_type TEXT NOT NULL,
  version TEXT NOT NULL,
  schema JSONB NOT NULL,
  checksum TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (event_type, version)
);
`

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	// Ensure migrations applied by touching key tables.
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='users')`).Scan(&exists); err != nil {
		return fmt.Errorf("sanity check: %w", err)
	}
	if !exists {
		return fmt.Errorf("users table missing after migrations")
	}
	return nil
}

func seedTopic(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1,$2,$3)`, seededUserID, "integration@example.com", "hash"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO topics (id, user_id, name, preferences, schedule_cron) VALUES ($1,$2,$3,'{}','@daily')`, seededTopicID, seededUserID, "Integration Topic")
	return err
}

func awaitCheckpoint(t *testing.T, ctx context.Context, st *store.Store, status string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cps, err := st.ListCheckpointsByStatus(ctx, status)
		if err != nil {
			t.Fatalf("list checkpoints: %v", err)
		}
		if len(cps) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("checkpoint with status %s not observed within timeout", status)
}
