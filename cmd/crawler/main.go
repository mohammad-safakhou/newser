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
		log.Fatalf("crawler schema registry init: %v", err)
	}

	if err := runtime.RunPlaceholder(ctx, "crawler"); err != nil {
		log.Fatalf("crawler service exited: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}
