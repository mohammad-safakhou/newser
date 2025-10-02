package runtime

import (
	"context"
	"reflect"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func TestToolCardRecordRoundTrip(t *testing.T) {
	original := capability.ToolCard{
		Name:        "demo",
		Version:     "v1.2.3",
		Description: "Example tool",
		AgentType:   "demo",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"foo"}},
		OutputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"foo": map[string]interface{}{"type": "string"}},
		},
		CostEstimate: 1.25,
		SideEffects:  []string{"network", "filesystem"},
	}
	checksum, err := capability.ComputeChecksum(original)
	if err != nil {
		t.Fatalf("ComputeChecksum: %v", err)
	}
	original.Checksum = checksum
	signature, err := capability.SignToolCard(original, "secret")
	if err != nil {
		t.Fatalf("SignToolCard: %v", err)
	}
	original.Signature = signature

	record, err := ToolCardRecordFromToolCard(original)
	if err != nil {
		t.Fatalf("ToolCardRecordFromToolCard: %v", err)
	}
	roundTrip, err := ToolCardFromRecord(record)
	if err != nil {
		t.Fatalf("ToolCardFromRecord: %v", err)
	}

	if roundTrip.Name != original.Name || roundTrip.Version != original.Version || roundTrip.AgentType != original.AgentType {
		t.Fatalf("basic fields mismatch: %#v != %#v", roundTrip, original)
	}
	if roundTrip.Description != original.Description || roundTrip.CostEstimate != original.CostEstimate {
		t.Fatalf("metadata mismatch: %#v != %#v", roundTrip, original)
	}
	if !reflect.DeepEqual(roundTrip.SideEffects, original.SideEffects) {
		t.Fatalf("side effects mismatch: %#v != %#v", roundTrip.SideEffects, original.SideEffects)
	}
	if !reflect.DeepEqual(roundTrip.InputSchema, original.InputSchema) {
		t.Fatalf("input schema mismatch: %#v != %#v", roundTrip.InputSchema, original.InputSchema)
	}
	if !reflect.DeepEqual(roundTrip.OutputSchema, original.OutputSchema) {
		t.Fatalf("output schema mismatch: %#v != %#v", roundTrip.OutputSchema, original.OutputSchema)
	}
	if roundTrip.Checksum != original.Checksum || roundTrip.Signature != original.Signature {
		t.Fatalf("crypto fields mismatch: %#v != %#v", roundTrip, original)
	}
}

func TestEnsureCapabilityRegistrySeedsDefaults(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &store.Store{DB: db}
	secret := "unit-test-secret"
	cfg := &config.Config{Capability: config.CapabilityConfig{SigningSecret: secret}}

	baseQuery := regexp.QuoteMeta("SELECT name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at FROM tool_registry ORDER BY name, version")
	insertStmt := regexp.QuoteMeta("INSERT INTO tool_registry (name, version, description, agent_type, input_schema, output_schema, cost_estimate, side_effects, checksum, signature, created_at)\nVALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())\nON CONFLICT (name, version) DO UPDATE SET\n  description = EXCLUDED.description,\n  agent_type = EXCLUDED.agent_type,\n  input_schema = EXCLUDED.input_schema,\n  output_schema = EXCLUDED.output_schema,\n  cost_estimate = EXCLUDED.cost_estimate,\n  side_effects = EXCLUDED.side_effects,\n  checksum = EXCLUDED.checksum,\n  signature = EXCLUDED.signature;\n")

	mock.ExpectQuery(baseQuery).WillReturnRows(sqlmock.NewRows([]string{"name", "version", "description", "agent_type", "input_schema", "output_schema", "cost_estimate", "side_effects", "checksum", "signature", "created_at"}))

	defaults := capability.DefaultToolCards()
	rows := sqlmock.NewRows([]string{"name", "version", "description", "agent_type", "input_schema", "output_schema", "cost_estimate", "side_effects", "checksum", "signature", "created_at"})

	now := time.Now()
	for _, tc := range defaults {
		checksum, err := capability.ComputeChecksum(tc)
		if err != nil {
			t.Fatalf("ComputeChecksum: %v", err)
		}
		tc.Checksum = checksum
		signature, err := capability.SignToolCard(tc, secret)
		if err != nil {
			t.Fatalf("SignToolCard: %v", err)
		}
		tc.Signature = signature

		rec, err := ToolCardRecordFromToolCard(tc)
		if err != nil {
			t.Fatalf("ToolCardRecordFromToolCard: %v", err)
		}

		mock.ExpectExec(insertStmt).WithArgs(rec.Name, rec.Version, rec.Description, rec.AgentType, rec.InputSchema, rec.OutputSchema, rec.CostEstimate, rec.SideEffects, rec.Checksum, rec.Signature).WillReturnResult(sqlmock.NewResult(0, 1))

		rows.AddRow(rec.Name, rec.Version, rec.Description, rec.AgentType, rec.InputSchema, rec.OutputSchema, rec.CostEstimate, rec.SideEffects, rec.Checksum, rec.Signature, now)
	}

	mock.ExpectQuery(baseQuery).WillReturnRows(rows)

	registry, err := EnsureCapabilityRegistry(context.Background(), st, cfg)
	if err != nil {
		t.Fatalf("EnsureCapabilityRegistry: %v", err)
	}

	for _, required := range []string{"research", "analysis", "synthesis", "conflict_detection", "highlight_management", "knowledge_graph"} {
		if _, ok := registry.Tool(required); !ok {
			t.Fatalf("expected tool %s to be registered", required)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
