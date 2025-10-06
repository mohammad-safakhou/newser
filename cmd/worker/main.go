package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/queue/streams"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/worker"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	flag.Parse()

	cfg := config.LoadConfig(*cfgPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.New(os.Stdout, "[WORKER] ", log.LstdFlags)

	if _, err := runtime.EnsureSandbox(ctx, cfg, "worker", logger, runtime.SandboxRequest{}); err != nil {
		log.Fatalf("worker sandbox: %v", err)
	}

	telemetry, meter, tracer, err := runtime.SetupTelemetry(ctx, cfg.Telemetry, runtime.TelemetryOptions{ServiceName: "worker", ServiceVersion: "dev", MetricsPort: cfg.Telemetry.MetricsPort + 1})
	if err != nil {
		log.Fatalf("worker telemetry init: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	st, registry, err := runtime.InitSchemaRegistry(ctx, cfg)
	if err != nil {
		log.Fatalf("worker registry init: %v", err)
	}

	redisAddr := fmt.Sprintf("%s:%s", cfg.Storage.Redis.Host, cfg.Storage.Redis.Port)
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: cfg.Storage.Redis.Password, DB: cfg.Storage.Redis.DB})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("worker redis ping: %v", err)
	}
	defer func() { _ = rdb.Close() }()

	const groupName = "worker-group"
	if err := streams.EnsureGroup(ctx, rdb, worker.StreamRunEnqueued, groupName); err != nil {
		log.Fatalf("worker ensure group: %v", err)
	}

	consumerName := fmt.Sprintf("worker-%s", uuid.NewString()[:8])
	consumer := streams.NewConsumer(rdb, registry, groupName, consumerName)
	publisher := streams.NewPublisher(rdb, registry)

	processor := worker.NewProcessor(logger, st, publisher, consumer, worker.StreamRunEnqueued, worker.StreamTaskDispatch, meter, tracer)

	if err := processor.Start(ctx); err != nil {
		log.Fatalf("worker processor exited: %v", err)
	}
}
