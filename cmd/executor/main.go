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

	telemetry, _, _, err := runtime.SetupTelemetry(ctx, cfg.Telemetry, runtime.TelemetryOptions{ServiceName: "executor", ServiceVersion: "dev", MetricsPort: cfg.Telemetry.MetricsPort + 2})
	if err != nil {
		log.Fatalf("executor telemetry init: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	sandboxLogger := log.New(os.Stdout, "[EXECUTOR] ", log.LstdFlags)

	if _, _, err := runtime.EnsureSandbox(ctx, cfg, "executor", sandboxLogger, runtime.SandboxRequest{}); err != nil {
		log.Fatalf("executor sandbox: %v", err)
	}

	if _, _, err := runtime.InitSchemaRegistry(ctx, cfg); err != nil {
		log.Fatalf("executor schema registry init: %v", err)
	}

	if err := runtime.RunPlaceholder(ctx, "executor"); err != nil {
		log.Fatalf("executor service exited: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}
