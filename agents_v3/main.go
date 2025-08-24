package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/agents_v3/core"
	"github.com/mohammad-safakhou/newser/agents_v3/config"
	"github.com/mohammad-safakhou/newser/agents_v3/telemetry"
)

func main() {
	// Initialize logging and telemetry
	logger := log.New(os.Stdout, "[NEWSER-AGENT] ", log.LstdFlags|log.Lshortfile)
	
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Initialize telemetry system
	telemetry := telemetry.NewTelemetry(cfg.Telemetry)
	defer telemetry.Shutdown()

	// Create the main orchestrator agent
	orchestrator, err := core.NewOrchestrator(cfg, logger, telemetry)
	if err != nil {
		logger.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Example usage - in real implementation this would come from your main app
	userThought := core.UserThought{
		ID:        uuid.New().String(),
		Content:   "I want to stay updated on Golang development and any major releases or breaking changes",
		Timestamp: time.Now(),
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Process the user thought
	result, err := orchestrator.ProcessThought(ctx, userThought)
	if err != nil {
		logger.Printf("Error processing thought: %v", err)
		return
	}

	logger.Printf("Processing completed successfully")
	logger.Printf("Response: %s", result.Summary)
	logger.Printf("Sources: %d", len(result.Sources))
	logger.Printf("Processing time: %v", result.ProcessingTime)
	logger.Printf("Cost estimate: $%.4f", result.CostEstimate)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	
	logger.Println("Shutting down gracefully...")
}
