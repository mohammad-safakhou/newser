package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/runtime"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	flag.Parse()

	cfg := config.LoadConfig(*cfgPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	telemetry, _, _, err := runtime.SetupTelemetry(ctx, cfg.Telemetry, runtime.TelemetryOptions{ServiceName: "api", ServiceVersion: "dev", MetricsPort: cfg.Telemetry.MetricsPort})
	if err != nil {
		log.Fatalf("api telemetry init: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	if _, _, err := runtime.InitSchemaRegistry(ctx, cfg); err != nil {
		log.Fatalf("api schema registry init: %v", err)
	}

	if err := runtime.RunPlaceholder(ctx, "api"); err != nil {
		log.Fatalf("api service exited: %v", err)
	}

	// Sleep briefly to give deferred logs time to flush in containerised runs.
	time.Sleep(100 * time.Millisecond)
}
