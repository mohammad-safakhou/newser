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
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "seed":
		withSchemaStore(*cfgPath, func(ctx context.Context, cfg *config.Config, st *store.Store) {
			if err := schema.SeedBaseSchemas(ctx, st); err != nil {
				log.Fatalf("seed: %v", err)
			}
			fmt.Println("Seeded base schemas")
		})
	case "list":
		withSchemaStore(*cfgPath, func(ctx context.Context, cfg *config.Config, st *store.Store) {
			listSchemas(ctx, st)
		})
	case "publish":
		withSchemaStore(*cfgPath, func(ctx context.Context, cfg *config.Config, st *store.Store) {
			publishSchema(ctx, st, rest)
		})
	case "validate":
		if err := validateSchemaCmd(rest); err != nil {
			log.Fatalf("validate: %v", err)
		}
	case "diff":
		withSchemaStore(*cfgPath, func(ctx context.Context, cfg *config.Config, st *store.Store) {
			diffSchemasCmd(ctx, st, rest)
		})
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Usage: schema [--config=config.json] <command>
Commands:
  seed
  list
  publish --event EVENT --version VERSION --file schema.json
  validate --file schema.json
  diff --event EVENT (--version VERSION --against OTHER | --against VERSION --file schema.json)`)
}

func withSchemaStore(cfgPath string, fn func(ctx context.Context, cfg *config.Config, st *store.Store)) {
	cfg := config.LoadConfig(cfgPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn, err := runtime.BuildPostgresDSN(cfg)
	if err != nil {
		log.Fatalf("schema: %v", err)
	}
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		log.Fatalf("schema store: %v", err)
	}
	defer st.DB.Close()

	fn(ctx, cfg, st)
}

func listSchemas(ctx context.Context, st *store.Store) {
	records, err := st.ListMessageSchemas(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	if len(records) == 0 {
		fmt.Println("(no schemas registered)")
		return
	}
	for _, rec := range records {
		fmt.Printf("%s@%s checksum=%s created=%s\n", rec.EventType, rec.Version, rec.Checksum, rec.CreatedAt.UTC().Format(time.RFC3339))
	}
}

func publishSchema(ctx context.Context, st *store.Store, args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	eventType := fs.String("event", "", "event type")
	version := fs.String("version", "", "schema version")
	file := fs.String("file", "", "schema JSON file")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *eventType == "" || *version == "" || *file == "" {
		log.Fatal("publish requires --event, --version, and --file")
	}
	contents, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read schema file: %v", err)
	}
	if err := schema.Upsert(ctx, st, *eventType, *version, contents); err != nil {
		log.Fatalf("publish: %v", err)
	}
	fmt.Printf("Registered schema %s@%s\n", *eventType, *version)
}

func validateSchemaCmd(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	file := fs.String("file", "", "schema JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("validate requires --file")
	}
	contents, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}
	if err := schema.ValidateSchema(contents); err != nil {
		return err
	}
	fmt.Printf("Schema %s validated successfully\n", *file)
	return nil
}

func diffSchemasCmd(ctx context.Context, st *store.Store, args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	eventType := fs.String("event", "", "event type")
	version := fs.String("version", "", "stored schema version")
	against := fs.String("against", "", "version to diff against")
	file := fs.String("file", "", "schema JSON file to compare against stored version")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *eventType == "" {
		log.Fatal("diff requires --event")
	}
	if *file != "" {
		if *against == "" {
			log.Fatal("diff with --file also requires --against (stored version)")
		}
		contents, err := os.ReadFile(*file)
		if err != nil {
			log.Fatalf("read schema file: %v", err)
		}
		diff, err := schema.DiffCandidate(ctx, st, *eventType, *against, contents)
		if err != nil {
			log.Fatalf("diff: %v", err)
		}
		emitDiff(diff)
		return
	}
	if *version == "" || *against == "" {
		log.Fatal("diff requires --version and --against when --file is not provided")
	}
	diff, err := schema.DiffStored(ctx, st, *eventType, *version, *against)
	if err != nil {
		log.Fatalf("diff: %v", err)
	}
	emitDiff(diff)
}

func emitDiff(diff string) {
	if diff == "" {
		fmt.Println("Schemas are identical")
		return
	}
	fmt.Println(diff)
}
