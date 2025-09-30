package schema

import (
	"context"
	"fmt"

	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// Store defines the persistence contract required by the schema registry manager.
type Store interface {
	UpsertMessageSchema(ctx context.Context, eventType, version string, schemaBytes []byte) error
	ListMessageSchemas(ctx context.Context) ([]store.SchemaRecord, error)
}

// SeedBaseSchemas ensures the built-in schema definitions exist in the registry store.
func SeedBaseSchemas(ctx context.Context, st Store) error {
	if st == nil {
		return fmt.Errorf("store is nil")
	}
	for _, def := range streams.BaseDefinitions() {
		if err := st.UpsertMessageSchema(ctx, def.EventType, def.Version, def.Schema); err != nil {
			return fmt.Errorf("seed %s %s: %w", def.EventType, def.Version, err)
		}
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
