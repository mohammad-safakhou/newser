package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorymanager "github.com/mohammad-safakhou/newser/internal/memory/manager"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func runDeltaCommand(args []string) error {
	fs := flag.NewFlagSet("delta", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	inputPath := fs.String("input", "", "path to delta payload JSON (defaults to stdin)")
	topicOverride := fs.String("topic", "", "override topic identifier in payload")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload, err := readInput(*inputPath)
	if err != nil {
		return fmt.Errorf("read delta payload: %w", err)
	}

	var req memorysvc.DeltaRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("decode delta payload: %w", err)
	}
	if req.TopicID == "" && *topicOverride != "" {
		req.TopicID = *topicOverride
	}
	if req.TopicID == "" {
		return fmt.Errorf("delta payload missing topic_id")
	}

	return withMemoryManager(*cfgPath, false, func(ctx context.Context, cfg *config.Config, st *store.Store, provider agentcore.LLMProvider, mgr *memorymanager.Manager, _ []byte) error {
		resp, err := mgr.Delta(ctx, req)
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(out); err != nil {
			return err
		}
		_, err = os.Stdout.Write([]byte("\n"))
		return err
	})
}
