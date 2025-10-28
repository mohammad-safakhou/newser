package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type memoryRuntimeResources struct {
	telemetry *runtime.Telemetry
	store     *store.Store
	registry  *streams.SchemaRegistry
	jwtSecret []byte
}

func bootstrapMemoryRuntime(ctx context.Context, cfg *config.Config, opts runtime.TelemetryOptions) (*memoryRuntimeResources, error) {
	telemetry, _, _, err := runtime.SetupTelemetry(ctx, cfg.Telemetry, opts)
	if err != nil {
		return nil, fmt.Errorf("telemetry init: %w", err)
	}

	st, registry, err := runtime.InitSchemaRegistry(ctx, cfg)
	if err != nil {
		shutdownTelemetry(telemetry)
		return nil, fmt.Errorf("schema registry init: %w", err)
	}

	secret, err := runtime.LoadJWTSecret(cfg)
	if err != nil {
		_ = st.DB.Close()
		shutdownTelemetry(telemetry)
		return nil, fmt.Errorf("load jwt secret: %w", err)
	}

	return &memoryRuntimeResources{
		telemetry: telemetry,
		store:     st,
		registry:  registry,
		jwtSecret: secret,
	}, nil
}

func (r *memoryRuntimeResources) Shutdown() {
	if r == nil {
		return
	}
	if r.store != nil && r.store.DB != nil {
		_ = r.store.DB.Close()
	}
	shutdownTelemetry(r.telemetry)
}

func shutdownTelemetry(tel *runtime.Telemetry) {
	if tel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tel.Shutdown(ctx)
}
