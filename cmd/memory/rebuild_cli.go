package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func runRebuildCLI(args []string) error {
	fs := flag.NewFlagSet("rebuild-embeddings", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	batchSize := fs.Int("batch", 100, "batch size for rebuild operations")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return withMemoryEnv(*cfgPath, true, func(ctx context.Context, cfg *config.Config, st *store.Store, provider agentcore.LLMProvider, _ []byte) error {
		if !cfg.Memory.Semantic.Enabled {
			return fmt.Errorf("semantic memory disabled in configuration")
		}
		rebuildLogger := log.New(os.Stdout, "[MEMORY REBUILD] ", log.LstdFlags)
		ingestor, err := semantic.NewIngestor(st, provider, cfg.Memory.Semantic, rebuildLogger)
		if err != nil {
			return err
		}
		if ingestor == nil {
			return fmt.Errorf("semantic ingestor unavailable")
		}
		total, err := rebuildSemanticEmbeddings(ctx, st, ingestor, *batchSize, rebuildLogger)
		if err != nil {
			return err
		}
		fmt.Printf("semantic rebuild processed %d runs\n", total)
		return nil
	})
}
