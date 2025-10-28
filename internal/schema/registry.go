package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/store"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

// Store defines the persistence contract required by the schema registry manager.
type Store interface {
	UpsertMessageSchema(ctx context.Context, eventType, version string, schemaBytes []byte) error
	ListMessageSchemas(ctx context.Context) ([]store.SchemaRecord, error)
	GetMessageSchema(ctx context.Context, eventType, version string) (store.SchemaRecord, bool, error)
}

// SeedBaseSchemas ensures the built-in schema definitions exist in the registry store.
func SeedBaseSchemas(ctx context.Context, st Store) error {
	if st == nil {
		return fmt.Errorf("store is nil")
	}
	for _, def := range streams.BaseDefinitions() {
		if err := ValidateSchema(def.Schema); err != nil {
			return fmt.Errorf("validate base schema %s %s: %w", def.EventType, def.Version, err)
		}
		if err := st.UpsertMessageSchema(ctx, def.EventType, def.Version, def.Schema); err != nil {
			return fmt.Errorf("seed %s %s: %w", def.EventType, def.Version, err)
		}
	}
	if err := SeedRunManifestSchema(ctx, st); err != nil {
		return err
	}
	return nil
}

// Load constructs a runtime schema registry from persisted definitions.
func Load(ctx context.Context, st Store) (*streams.SchemaRegistry, error) {
	if st == nil {
		return nil, fmt.Errorf("store is nil")
	}
	records, err := st.ListMessageSchemas(ctx)
	if err != nil {
		return nil, err
	}
	reg := streams.NewSchemaRegistry()
	for _, rec := range records {
		if err := reg.Register(rec.EventType, rec.Version, rec.Schema); err != nil {
			return nil, fmt.Errorf("register %s %s: %w", rec.EventType, rec.Version, err)
		}
	}
	return reg, nil
}

// Upsert validates and persists a schema definition.
func Upsert(ctx context.Context, st Store, eventType, version string, schemaBytes []byte) error {
	if err := ValidateSchema(schemaBytes); err != nil {
		return err
	}
	return st.UpsertMessageSchema(ctx, eventType, version, schemaBytes)
}

// ValidateSchema ensures schemaBytes compile as a JSON Schema document.
func ValidateSchema(schemaBytes []byte) error {
	if len(schemaBytes) == 0 {
		return fmt.Errorf("schemaBytes is empty")
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	if _, err := compiler.Compile("schema.json"); err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return nil
}

// DiffCandidate compares the stored schema version to a candidate definition.
func DiffCandidate(ctx context.Context, st Store, eventType, version string, candidate []byte) (string, error) {
	if err := ValidateSchema(candidate); err != nil {
		return "", err
	}
	rec, ok, err := st.GetMessageSchema(ctx, eventType, version)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no stored schema for %s@%s", eventType, version)
	}
	return diffSchemas(rec.Schema, candidate)
}

// DiffStored compares two stored schema versions for the same event type.
func DiffStored(ctx context.Context, st Store, eventType, versionA, versionB string) (string, error) {
	if versionA == versionB {
		return "", fmt.Errorf("versions are identical")
	}
	recA, ok, err := st.GetMessageSchema(ctx, eventType, versionA)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no stored schema for %s@%s", eventType, versionA)
	}
	recB, ok, err := st.GetMessageSchema(ctx, eventType, versionB)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no stored schema for %s@%s", eventType, versionB)
	}
	return diffSchemas(recA.Schema, recB.Schema)
}

func diffSchemas(a, b []byte) (string, error) {
	left, err := normalizeJSON(a)
	if err != nil {
		return "", fmt.Errorf("normalize base schema: %w", err)
	}
	right, err := normalizeJSON(b)
	if err != nil {
		return "", fmt.Errorf("normalize candidate schema: %w", err)
	}
	if left == right {
		return "", nil
	}
	return lineDiff(left, right), nil
}

func normalizeJSON(data []byte) (string, error) {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}
	normalized, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal indent: %w", err)
	}
	return string(normalized), nil
}

func lineDiff(base, candidate string) string {
	baseLines := strings.Split(base, "\n")
	candidateLines := strings.Split(candidate, "\n")
	max := len(baseLines)
	if len(candidateLines) > max {
		max = len(candidateLines)
	}
	var buf strings.Builder
	buf.WriteString("--- base\n")
	buf.WriteString(base)
	buf.WriteString("\n+++ candidate\n")
	buf.WriteString(candidate)
	buf.WriteString("\n@@ diff @@\n")
	for i := 0; i < max; i++ {
		var left, right string
		if i < len(baseLines) {
			left = baseLines[i]
		}
		if i < len(candidateLines) {
			right = candidateLines[i]
		}
		if left == right {
			buf.WriteString("  " + left + "\n")
			continue
		}
		if left != "" {
			buf.WriteString("- " + left + "\n")
		}
		if right != "" {
			buf.WriteString("+ " + right + "\n")
		}
	}
	return buf.String()
}
