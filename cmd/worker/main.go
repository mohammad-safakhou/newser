package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	logger := log.New(os.Stdout, "[WORKER] ", log.LstdFlags)
	processor := worker.NewProcessor(logger, st, publisher, consumer, worker.StreamRunEnqueued, worker.StreamTaskDispatch)

	if err := processor.Start(ctx); err != nil {
		log.Fatalf("worker processor exited: %v", err)
	}
}
