package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorymanager "github.com/mohammad-safakhou/newser/internal/memory/manager"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func withMemoryEnv(cfgPath string, requireSemantic bool, fn func(context.Context, *config.Config, *store.Store, agentcore.LLMProvider, []byte) error) error {
	if strings.TrimSpace(cfgPath) == "" {
		cfgPath = defaultConfigPath
	}
	cfg := config.LoadConfig(cfgPath)
	ctx := context.Background()

	resources, err := bootstrapMemoryRuntime(ctx, cfg, runtime.TelemetryOptions{
		ServiceName:    "memory-cli",
		ServiceVersion: "dev",
	})
	if err != nil {
		return err
	}
	defer resources.Shutdown()
	st := resources.store

	provider, err := agentcore.NewLLMProvider(cfg.LLM)
	if err != nil {
		if requireSemantic {
			return fmt.Errorf("llm provider init: %w", err)
		}
		provider = nil
	}

	return fn(ctx, cfg, st, provider, resources.jwtSecret)
}

func withMemoryManager(cfgPath string, requireSemantic bool, fn func(context.Context, *config.Config, *store.Store, agentcore.LLMProvider, *memorymanager.Manager, []byte) error) error {
	return withMemoryEnv(cfgPath, requireSemantic, func(ctx context.Context, cfg *config.Config, st *store.Store, provider agentcore.LLMProvider, secret []byte) error {
		mgr := memorymanager.New(st, cfg.Memory, provider, log.New(os.Stdout, "[MEMORY] ", log.LstdFlags))
		if mgr == nil {
			return fmt.Errorf("memory manager disabled for current configuration")
		}
		return fn(ctx, cfg, st, provider, mgr, secret)
	})
}

func readInput(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
