package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/schema"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	seed := flag.Bool("seed", false, "seed built-in schemas")
	list := flag.Bool("list", false, "list registered schemas")
	eventType := flag.String("event", "", "event type for register/update")
	version := flag.String("version", "", "schema version (e.g. v1)")
	file := flag.String("file", "", "path to schema JSON for register/update")
	flag.Parse()

	actions := 0
	if *seed {
		actions++
	}
	if *list {
		actions++
	}
	if *eventType != "" || *version != "" || *file != "" {
		actions++
	}
	if actions == 0 {
		flag.Usage()
		os.Exit(1)
	}

	cfg := config.LoadConfig(*cfgPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn, err := runtime.BuildPostgresDSN(cfg)
	if err != nil {
		log.Fatalf("schema: %v", err)
	}
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		log.Fatalf("schema store init: %v", err)
	}

	if *seed {
		if err := schema.SeedBaseSchemas(ctx, st); err != nil {
			log.Fatalf("seed base schemas: %v", err)
		}
		log.Println("Seeded base schemas")
	}

	if *eventType != "" || *version != "" || *file != "" {
		if *eventType == "" || *version == "" || *file == "" {
			log.Fatalf("event, version, and file must all be provided to register a schema")
		}
		contents, err := os.ReadFile(*file)
		if err != nil {
			log.Fatalf("read schema file: %v", err)
		}
		if err := st.UpsertMessageSchema(ctx, *eventType, *version, contents); err != nil {
			log.Fatalf("register schema: %v", err)
		}
		log.Printf("Registered schema %s@%s", *eventType, *version)
	}

	if *list {
		records, err := st.ListMessageSchemas(ctx)
		if err != nil {
			log.Fatalf("list schemas: %v", err)
		}
		if len(records) == 0 {
			fmt.Println("(no schemas registered)")
			return
		}
		for _, rec := range records {
			fmt.Printf("%s@%s checksum=%s created=%s\n", rec.EventType, rec.Version, rec.Checksum, rec.CreatedAt.UTC().Format(time.RFC3339))
		}
	}
}
