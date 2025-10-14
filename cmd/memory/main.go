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
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	flag.Parse()

	cfg := config.LoadConfig(*cfgPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	telemetry, _, _, err := runtime.SetupTelemetry(ctx, cfg.Telemetry, runtime.TelemetryOptions{ServiceName: "memory", ServiceVersion: "dev", MetricsPort: cfg.Telemetry.MetricsPort + 4})
	if err != nil {
		log.Fatalf("memory telemetry init: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	sandboxLogger := log.New(os.Stdout, "[MEMORY] ", log.LstdFlags)
	if _, _, err := runtime.EnsureSandbox(ctx, cfg, "memory", sandboxLogger, runtime.SandboxRequest{}); err != nil {
		log.Fatalf("memory sandbox: %v", err)
	}

	if _, _, err := runtime.InitSchemaRegistry(ctx, cfg); err != nil {
		log.Fatalf("memory schema registry init: %v", err)
	}

	if cfg.Memory.Semantic.Enabled && cfg.Memory.Semantic.RebuildOnStartup {
		dsn, err := runtime.BuildPostgresDSN(cfg)
		if err != nil {
			log.Fatalf("postgres configuration invalid: %v", err)
		}

		st, err := store.NewWithDSN(ctx, dsn)
		if err != nil {
			log.Fatalf("connect store: %v", err)
		}
		defer st.DB.Close()

		llmProvider, err := agentcore.NewLLMProvider(cfg.LLM)
		if err != nil {
			log.Fatalf("llm provider init: %v", err)
		}

		rebuildLogger := log.New(os.Stdout, "[MEMORY REBUILD] ", log.LstdFlags)
		ingestor, err := semantic.NewIngestor(st, llmProvider, cfg.Memory.Semantic, rebuildLogger)
		if err != nil {
			log.Fatalf("semantic ingestor init: %v", err)
		}
		if ingestor == nil {
			log.Fatalf("semantic ingestor unexpectedly disabled")
		}

		if _, err := rebuildSemanticEmbeddings(ctx, st, ingestor, cfg.Memory.Semantic.WriterBatchSize, rebuildLogger); err != nil {
			log.Fatalf("semantic rebuild failed: %v", err)
		}
	}

	if err := runtime.RunPlaceholder(ctx, "memory"); err != nil {
		log.Fatalf("memory service exited: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}
