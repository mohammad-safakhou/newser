package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func setupToolStore(t *testing.T) (*store.Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	cleanup := func() { db.Close() }
	return &store.Store{DB: db}, mock, cleanup
}

func TestToolsHandlerList(t *testing.T) {
	st, mock, cleanup := setupToolStore(t)
	defer cleanup()

	secret := "test-secret"
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: secret}}
	handler := &ToolsHandler{Store: st, Config: cfg}

	tc := capability.DefaultToolCards()[0]
	checksum, err := capability.ComputeChecksum(tc)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	tc.Checksum = checksum
	sig, err := capability.SignToolCard(tc, secret)
	if err != nil {
		t.Fatalf("SignToolCard: %v", err)
	}
	tc.Signature = sig

	rec, err := runtime.ToolCardRecordFromToolCard(tc)
	if err != nil {
		t.Fatalf("ToolCardRecordFromToolCard: %v", err)
	}

	rows := sqlmock.NewRows([]string{"name", "version", "description", "agent_type", "input_schema", "output_schema", "cost_estimate", "side_effects", "checksum", "signature", "created_at"}).
		AddRow(rec.Name, rec.Version, rec.Description, rec.AgentType, rec.InputSchema, rec.OutputSchema, rec.CostEstimate, rec.SideEffects, rec.Checksum, rec.Signature, time.Now())

	query := regexp.QuoteMeta("SELECT name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at FROM tool_registry ORDER BY name, version")
	mock.ExpectQuery(query).WillReturnRows(rows)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	recResp := httptest.NewRecorder()
	ctx := e.NewContext(req, recResp)

	if err := handler.list(ctx); err != nil {
		t.Fatalf("list: %v", err)
	}

	if recResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recResp.Code)
	}

	var payload []capability.ToolCard
	if err := json.Unmarshal(recResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 tool card, got %d", len(payload))
	}
	if payload[0].Signature != tc.Signature {
		t.Fatalf("expected signature %s, got %s", tc.Signature, payload[0].Signature)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestToolsHandlerPublish(t *testing.T) {
	st, mock, cleanup := setupToolStore(t)
	defer cleanup()

	secret := "test-secret"
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: secret}}
	handler := &ToolsHandler{Store: st, Config: cfg}

	tc := capability.ToolCard{
		Name:        "analysis",
		Version:     "v1.1.0",
		Description: "analysis agent",
		AgentType:   "analysis",
	}
	checksum, err := capability.ComputeChecksum(tc)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	tc.Checksum = checksum
	sig, err := capability.SignToolCard(tc, secret)
	if err != nil {
		t.Fatalf("SignToolCard: %v", err)
	}
	tc.Signature = sig
	recCard, err := runtime.ToolCardRecordFromToolCard(tc)
	if err != nil {
		t.Fatalf("ToolCardRecordFromToolCard: %v", err)
	}

	insert := regexp.QuoteMeta(`
INSERT INTO tool_registry (name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
ON CONFLICT (name, version) DO UPDATE SET
  description = EXCLUDED.description,
  agent_type = EXCLUDED.agent_type,
  input_schema = EXCLUDED.input_schema,
  output_schema = EXCLUDED.output_schema,
  cost_estimate = EXCLUDED.cost_estimate,
  side_effects = EXCLUDED.side_effects,
  checksum = EXCLUDED.checksum,
  signature = EXCLUDED.signature;
`)
	mock.ExpectExec(insert).WithArgs(recCard.Name, recCard.Version, recCard.Description, recCard.AgentType, recCard.InputSchema, recCard.OutputSchema, recCard.CostEstimate, recCard.SideEffects, recCard.Checksum, recCard.Signature).WillReturnResult(sqlmock.NewResult(0, 1))

	payload := map[string]interface{}{
		"tool_card": map[string]interface{}{
			"name":        "analysis",
			"version":     "v1.1.0",
			"description": "analysis agent",
			"agent_type":  "analysis",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/tools", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recResp := httptest.NewRecorder()
	ctx := e.NewContext(req, recResp)

	if err := handler.publish(ctx); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if recResp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", recResp.Code)
	}

	var resp capability.ToolCard
	if err := json.Unmarshal(recResp.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Signature != tc.Signature {
		t.Fatalf("expected signature %s, got %s", tc.Signature, resp.Signature)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestToolsHandlerPublishRejectsChecksumMismatch(t *testing.T) {
	st, mock, cleanup := setupToolStore(t)
	defer cleanup()

	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: "test-secret"}}
	handler := &ToolsHandler{Store: st, Config: cfg}

	payload := map[string]interface{}{
		"tool_card": map[string]interface{}{
			"name":       "analysis",
			"version":    "v1.0.0",
			"agent_type": "analysis",
			"checksum":   "bad",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/tools", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recResp := httptest.NewRecorder()
	ctx := e.NewContext(req, recResp)

	err = handler.publish(ctx)
	if err == nil {
		t.Fatalf("expected error due to checksum mismatch")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", httpErr.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
