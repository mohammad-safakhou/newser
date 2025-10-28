package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorymanager "github.com/mohammad-safakhou/newser/internal/memory/manager"
	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/server"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultConfigPath   = "config/config.json"
	defaultListenAddr   = ":10005"
	defaultTickInterval = 30 * time.Minute
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "delta":
			if err := runDeltaCommand(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "rebuild-embeddings":
			if err := runRebuildCLI(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "serve":
			if err := runServeCommand(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	if err := runServeCommand(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runServeCommand(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	address := fs.String("address", "", "HTTP listen address override for the memory service")
	rebuildSemantic := fs.Bool("rebuild-embeddings", false, "rebuild semantic embeddings on startup before serving traffic")
	tickInterval := fs.Duration("tick-interval", defaultTickInterval, "scheduler tick interval")
	summaryInterval := fs.Duration("summary-interval", 24*time.Hour, "minimum interval between topic summarisation runs")
	pruneInterval := fs.Duration("prune-interval", 12*time.Hour, "minimum interval between episodic pruning runs")
	enableSemantic := fs.Bool("enable-semantic-memory", false, "force enable semantic memory APIs")
	disableSemantic := fs.Bool("disable-semantic-memory", false, "force disable semantic memory APIs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.LoadConfig(*cfgPath)
	if *enableSemantic && *disableSemantic {
		return fmt.Errorf("enable-semantic-memory and disable-semantic-memory cannot both be set")
	}
	if *enableSemantic {
		cfg.Memory.Semantic.Enabled = true
	}
	if *disableSemantic {
		cfg.Memory.Semantic.Enabled = false
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sandboxLogger := log.New(os.Stdout, "[MEMORY] ", log.LstdFlags)
	if _, _, err := runtime.EnsureSandbox(ctx, cfg, "memory", sandboxLogger, runtime.SandboxRequest{}); err != nil {
		return fmt.Errorf("memory sandbox: %w", err)
	}
	resources, err := bootstrapMemoryRuntime(ctx, cfg, runtime.TelemetryOptions{
		ServiceName:    "memory",
		ServiceVersion: "dev",
		MetricsPort:    cfg.Telemetry.MetricsPort + 4,
	})
	if err != nil {
		return err
	}
	defer resources.Shutdown()
	st := resources.store
	secret := resources.jwtSecret

	llmProvider, err := agentcore.NewLLMProvider(cfg.LLM)
	if err != nil {
		if cfg.Memory.Semantic.Enabled {
			return fmt.Errorf("llm provider init: %w", err)
		}
		llmProvider = nil
	}

	mgrLogger := log.New(os.Stdout, "[MEMORY-MANAGER] ", log.LstdFlags)
	mgr := memorymanager.New(st, cfg.Memory, llmProvider, mgrLogger)
	if mgr == nil {
		log.Println("memory manager disabled (episodic and semantic memory disabled)")
	}

	if cfg.Memory.Semantic.Enabled && (cfg.Memory.Semantic.RebuildOnStartup || *rebuildSemantic) && llmProvider != nil {
		rebuildLogger := log.New(os.Stdout, "[MEMORY REBUILD] ", log.LstdFlags)
		ingestor, err := semantic.NewIngestor(st, llmProvider, cfg.Memory.Semantic, rebuildLogger)
		if err != nil {
			return fmt.Errorf("semantic ingestor init: %w", err)
		}
		if ingestor != nil {
			if _, err := rebuildSemanticEmbeddings(ctx, st, ingestor, cfg.Memory.Semantic.WriterBatchSize, rebuildLogger); err != nil {
				return fmt.Errorf("semantic rebuild failed: %w", err)
			}
		}
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.GET("/healthz", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	memoryHandler := server.NewMemoryHandler(cfg, st, llmProvider, mgr, log.New(os.Stdout, "[MEMORY HTTP] ", log.LstdFlags))
	if memoryHandler != nil {
		memoryHandler.Register(e.Group("/memory"), secret)
	} else {
		log.Println("memory handler unavailable; semantic and episodic endpoints disabled")
	}

	schedLogger := log.New(os.Stdout, "[MEMORY SCHED] ", log.LstdFlags)
	scheduler := newMemoryScheduler(cfg.Memory, st, mgr, schedLogger, memorySchedulerOptions{
		TickInterval:    *tickInterval,
		SummaryInterval: *summaryInterval,
		PruneInterval:   *pruneInterval,
	})
	if scheduler != nil {
		scheduler.Start(ctx)
	}

	addr := cfg.Server.Address
	if addr == "" {
		addr = defaultListenAddr
	}
	if *address != "" {
		addr = *address
	}
	srv := &http.Server{Addr: addr, Handler: e}

	errCh := make(chan error, 1)
	go func() {
		errCh <- e.StartServer(srv)
	}()

	log.Printf("memory service listening on %s", addr)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.Shutdown(shutdownCtx)
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}
