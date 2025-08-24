package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/internal/agent/config"
	"github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
)

func main() {
	logger := log.New(os.Stdout, "[POLITICAL] ", log.LstdFlags|log.Lshortfile)
	logger.Println("=== POLITICAL NEWS AGENT (single scenario) ===")

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatalf("config load: %v", err)
	}

	tele := telemetry.NewTelemetry(cfg.Telemetry)
	defer tele.Shutdown()

	orch, err := core.NewOrchestrator(cfg, logger, tele)
	if err != nil {
		logger.Fatalf("orchestrator: %v", err)
	}

	thought := core.UserThought{
		ID:        uuid.New().String(),
		Content:   "I want balanced political news coverage in the US, across liberal and conservative sources, with bias and conflict detection.",
		Timestamp: time.Now(),
		Preferences: map[string]interface{}{
			"political_balance": "required",
			"source_diversity":  "high",
			"bias_detection":    true,
			"conflict_analysis": true,
		},
		Context: map[string]interface{}{
			"user_location": "US",
			"interests":     []string{"politics", "policy", "elections"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	start := time.Now()
	res, err := orch.ProcessThought(ctx, thought)
	elapsed := time.Since(start)
	if err != nil {
		logger.Fatalf("process: %v", err)
	}

	logger.Printf("Processing Time: %v", elapsed)
	logger.Printf("Cost: $%.4f | Tokens: %d | Confidence: %.2f", res.CostEstimate, res.TokensUsed, res.Confidence)
	logger.Printf("Agents Used: %v | Models: %v", res.AgentsUsed, res.LLMModelsUsed)
	logger.Printf("\nSUMMARY:\n%s", res.Summary)

	logger.Printf("\nWhole object response:\n%+v", res)
}
