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

	if _, _, err := runtime.InitSchemaRegistry(ctx, cfg); err != nil {
		log.Fatalf("api schema registry init: %v", err)
	}

	if err := runtime.RunPlaceholder(ctx, "api"); err != nil {
		log.Fatalf("api service exited: %v", err)
	}

	// Sleep briefly to give deferred logs time to flush in containerised runs.
	time.Sleep(100 * time.Millisecond)
}
