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
	concurrency := flag.Int("concurrency", 1, "number of runs to process concurrently (>=1)")
	resumeInterval := flag.Duration("resume-interval", 0, "how often to reclaim stale checkpoints (0 disables)")
	backpressurePending := flag.Int("backpressure-pending", 0, "pending run threshold to trigger backpressure (0 disables)")
	backpressureSleep := flag.Duration("backpressure-sleep", 5*time.Second, "sleep duration when backpressure engages")
	smokeResume := flag.Bool("smoke-resume", false, "run resume flow once, logging summary, then exit")
	flag.Parse()

	cfg := config.LoadConfig(*cfgPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.New(os.Stdout, "[WORKER] ", log.LstdFlags)

	if _, _, err := runtime.EnsureSandbox(ctx, cfg, "worker", logger, runtime.SandboxRequest{}); err != nil {
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

	var procOpts []worker.ProcessorOption
	procOpts = append(procOpts, worker.WithMaxConcurrency(*concurrency))
	if *concurrency > 1 {
		procOpts = append(procOpts, worker.WithReadBatchSize(int64(*concurrency)))
	}
	if *resumeInterval > 0 {
		procOpts = append(procOpts, worker.WithResumeInterval(*resumeInterval))
	}
	if *backpressurePending > 0 {
		sleep := *backpressureSleep
		if sleep <= 0 {
			sleep = 5 * time.Second
		}
		procOpts = append(procOpts, worker.WithBackpressureThreshold(int64(*backpressurePending), sleep))
	}
	processor := worker.NewProcessor(logger, st, publisher, consumer, worker.StreamRunEnqueued, worker.StreamTaskDispatch, meter, tracer, procOpts...)

	if *smokeResume {
		stats, err := processor.ResumePending(ctx)
		if err != nil {
			log.Fatalf("worker smoke-resume failed: %v", err)
		}
		logger.Printf("smoke resume summary: checkpoints=%d reclaimed=%d skipped_for_approval=%d", stats.Checkpoints, stats.Reclaimed, stats.SkippedForApproval)
		return
	}

	if err := processor.Start(ctx); err != nil {
		log.Fatalf("worker processor exited: %v", err)
	}
}
