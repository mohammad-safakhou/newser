package runtime

import (
	"context"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/schema"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// InitSchemaRegistry ensures base schemas exist and returns a populated runtime registry along with the store handle.
func InitSchemaRegistry(ctx context.Context, cfg *config.Config) (*store.Store, *streams.SchemaRegistry, error) {
	dsn, err := BuildPostgresDSN(cfg)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	if err := schema.SeedBaseSchemas(ctx, st); err != nil {
		return nil, nil, err
	}
	reg, err := schema.Load(ctx, st)
	if err != nil {
		return nil, nil, err
	}
	return st, reg, nil
}
