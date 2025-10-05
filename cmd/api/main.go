package main

import (
	"context"
	"flag"
	"log"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	srv "github.com/mohammad-safakhou/newser/internal/server"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	planEstimateMode := flag.String("plan-estimate-mode", "", "plan estimation mode (auto|document|task-sum|none)")
	planDryRunEnabled := flag.Bool("enable-plan-dry-run", true, "expose /api/plans/dry-run endpoint")
	enableSemanticMemory := flag.Bool("enable-semantic-memory", false, "force enable semantic memory endpoints")
	disableSemanticMemory := flag.Bool("disable-semantic-memory", false, "force disable semantic memory endpoints")
	flag.Parse()

	cfg := config.LoadConfig(*cfgPath)
	flag.CommandLine.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "plan-estimate-mode":
			cfg.Server.PlanEstimateMode = strings.TrimSpace(*planEstimateMode)
		case "enable-plan-dry-run":
			cfg.Server.PlanDryRunEnabled = *planDryRunEnabled
		case "enable-semantic-memory":
			cfg.Memory.Semantic.Enabled = true
		case "disable-semantic-memory":
			cfg.Memory.Semantic.Enabled = false
		}
	})

	if *enableSemanticMemory && *disableSemanticMemory {
		log.Fatal("enable-semantic-memory and disable-semantic-memory cannot both be set")
	}

	switch cfg.Server.PlanEstimateMode {
	case "", srv.PlanEstimateModeAuto, srv.PlanEstimateModeDocument, srv.PlanEstimateModeTaskSum, srv.PlanEstimateModeNone:
		if cfg.Server.PlanEstimateMode == "" {
			cfg.Server.PlanEstimateMode = srv.PlanEstimateModeAuto
		}
	default:
		log.Fatalf("invalid plan estimate mode: %s", cfg.Server.PlanEstimateMode)
	}

	ctx := context.Background()

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

	if err := srv.Run(cfg); err != nil {
		log.Fatalf("api service exited: %v", err)
	}

	// Sleep briefly to give deferred logs time to flush in containerised runs.
	time.Sleep(100 * time.Millisecond)
}
