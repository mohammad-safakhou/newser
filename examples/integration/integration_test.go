package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/server"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func startPostgres(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "newser",
			"POSTGRES_PASSWORD": "newser",
			"POSTGRES_DB":       "newser",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(1).WithStartupTimeout(60 * time.Second),
	}
	pg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("failed to start postgres: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432")
	if err != nil {
		_ = pg.Terminate(ctx)
		t.Fatalf("failed to get mapped port: %v", err)
	}
	host, err := pg.Host(ctx)
	if err != nil {
		_ = pg.Terminate(ctx)
		t.Fatalf("failed to get host: %v", err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", "newser", "newser", host, port.Port(), "newser")
	return pg, dsn
}

func findMigrationsDir(t *testing.T) string {
	t.Helper()
	cwd, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(cwd, "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return "file://" + candidate
		}
		cwd = filepath.Dir(cwd)
	}
	t.Fatalf("could not locate migrations directory from test cwd")
	return ""
}

func newServer(t *testing.T, ctx context.Context) (*httptest.Server, *store.Store, []byte) {
	t.Helper()
	// DB envs are already set by caller
	// run migrations explicitly, retry a few times for readiness
	migDir := findMigrationsDir(t)
	var migErr error
	for i := 0; i < 6; i++ {
		migErr = server.Migrate(migDir, "", "up", 0)
		if migErr == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if migErr != nil {
		t.Fatalf("migrate up failed after retries: %v", migErr)
	}
	st, err := store.New(ctx)
	if err != nil {
		t.Fatalf("store new failed: %v", err)
	}
	secret := []byte("test-secret")

	e := echo.New()
	api := e.Group("/api")

	auth := &server.AuthHandler{Store: st, Secret: secret}
	auth.Register(api.Group("/auth"))

	th := &server.TopicsHandler{Store: st}
	th.Register(api.Group("/topics"), secret)

	rh := &server.runsHandler{store: st}
	rh.Register(api.Group("/topics"), secret)

	srv := httptest.NewServer(e)
	return srv, st, secret
}

func TestAuthTopicsRunsFlow(t *testing.T) {
	ctx := context.Background()
	pg, _ := startPostgres(t, ctx)
	defer func() { _ = pg.Terminate(ctx) }()

	// export envs for server/store
	// Note: store.New uses individual envs if DATABASE_URL is empty
	// so we set POSTGRES_* vars
	os.Setenv("POSTGRES_HOST", "localhost")
	// testcontainers maps to host network via forwarded port; override port below
	port, err := pg.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("map port: %v", err)
	}
	os.Setenv("POSTGRES_PORT", port.Port())
	os.Setenv("POSTGRES_USER", "newser")
	os.Setenv("POSTGRES_PASSWORD", "newser")
	os.Setenv("POSTGRES_DB", "newser")

	srv, _, _ := newServer(t, ctx)
	defer srv.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// signup
	{
		body := map[string]string{"Email": "alice@example.com", "Password": "verysecure"}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/api/auth/signup", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("signup request failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 for signup, got %d", res.StatusCode)
		}
	}

	// login
	var token string
	{
		body := map[string]string{"Email": "alice@example.com", "Password": "verysecure"}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/api/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("login request failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for login, got %d", res.StatusCode)
		}
		var resp map[string]string
		_ = json.NewDecoder(res.Body).Decode(&resp)
		token = resp["token"]
		if token == "" {
			t.Fatalf("expected token in login response")
		}
	}

	// create topic
	var topicID string
	{
		payload := map[string]interface{}{
			"name":          "Test Topic",
			"preferences":   map[string]interface{}{"source": "test"},
			"schedule_cron": "@daily",
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", srv.URL+"/api/topics", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("create topic failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 for create topic, got %d", res.StatusCode)
		}
		var resp map[string]string
		_ = json.NewDecoder(res.Body).Decode(&resp)
		topicID = resp["id"]
		if topicID == "" {
			t.Fatalf("expected topic id")
		}
	}

	// list topics
	{
		req, _ := http.NewRequest("GET", srv.URL+"/api/topics", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("list topics failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 list topics, got %d", res.StatusCode)
		}
	}

	// trigger run (we only assert it is accepted and a run row is created)
	{
		req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/topics/%s/trigger", srv.URL, topicID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("trigger run failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("expected 202 trigger, got %d", res.StatusCode)
		}
	}

	// poll runs list until we see at least one entry or timeout
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/topics/%s/runs", srv.URL, topicID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("list runs failed: %v", err)
		}
		if res.StatusCode == http.StatusOK {
			var arr []map[string]interface{}
			_ = json.NewDecoder(res.Body).Decode(&arr)
			res.Body.Close()
			if len(arr) > 0 {
				break
			}
		}
		res.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run row")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestLatestResultAndUnauthorized(t *testing.T) {
	ctx := context.Background()
	pg, _ := startPostgres(t, ctx)
	defer func() { _ = pg.Terminate(ctx) }()

	os.Setenv("POSTGRES_HOST", "localhost")
	port, err := pg.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("map port: %v", err)
	}
	os.Setenv("POSTGRES_PORT", port.Port())
	os.Setenv("POSTGRES_USER", "newser")
	os.Setenv("POSTGRES_PASSWORD", "newser")
	os.Setenv("POSTGRES_DB", "newser")

	srv, st, _ := newServer(t, ctx)
	defer srv.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// unauthorized topics should be 401
	{
		req, _ := http.NewRequest("GET", srv.URL+"/api/topics", nil)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("unauth list topics failed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unauthorized topics, got %d", res.StatusCode)
		}
	}

	// signup/login
	var token string
	{
		b, _ := json.Marshal(map[string]string{"Email": "bob@example.com", "Password": "supersecret"})
		res, err := client.Post(srv.URL+"/api/auth/signup", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("signup: %v", err)
		}
		res.Body.Close()
		res, err = client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 login, got %d", res.StatusCode)
		}
		var resp map[string]string
		_ = json.NewDecoder(res.Body).Decode(&resp)
		token = resp["token"]
	}

	// create topic
	var topicID string
	{
		payload := map[string]interface{}{"name": "T2", "preferences": map[string]interface{}{}, "schedule_cron": "@daily"}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", srv.URL+"/api/topics", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("create topic: %v", err)
		}
		defer res.Body.Close()
		var j map[string]string
		_ = json.NewDecoder(res.Body).Decode(&j)
		topicID = j["id"]
	}

	// trigger to get a run id back
	var runID string
	{
		req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/topics/%s/trigger", srv.URL, topicID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("trigger: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("expected 202, got %d", res.StatusCode)
		}
		var j map[string]string
		_ = json.NewDecoder(res.Body).Decode(&j)
		runID = j["run_id"]
		if runID == "" {
			t.Fatalf("missing run_id")
		}
	}

	// Ensure scheduler lock code path can be exercised: mock minimal Redis with no-op by leaving REDIS unset here

	// upsert a minimal processing result for that run
	{
		pr := core.ProcessingResult{ID: runID, Summary: "ok", DetailedReport: "ok", Confidence: 0.9}
		if err := st.UpsertProcessingResult(ctx, pr); err != nil {
			t.Fatalf("upsert pr: %v", err)
		}
	}

	// fetch latest_result
	{
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/topics/%s/latest_result", srv.URL, topicID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("latest_result: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 latest_result, got %d", res.StatusCode)
		}
	}
}
