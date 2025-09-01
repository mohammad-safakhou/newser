package core

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
)

// Orchestrator coordinates all agents and manages the processing pipeline
type Orchestrator struct {
	config    *config.Config
	logger    *log.Logger
	telemetry *telemetry.Telemetry

	// Core components
	planner     *Planner
	agents      map[string]Agent
	llmProvider LLMProvider
	sources     []SourceProvider
	storage     Storage

	// Processing state
	processing map[string]*ProcessingStatus
	mu         sync.RWMutex

	// Concurrency control
	semaphore chan struct{}
}

// NewOrchestrator creates a new orchestrator instance
func NewOrchestrator(cfg *config.Config, logger *log.Logger, telemetry *telemetry.Telemetry) (*Orchestrator, error) {
	// Initialize LLM provider
	llmProvider, err := NewLLMProvider(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Initialize planner
	planner := NewPlanner(cfg, llmProvider, telemetry)

	// Initialize agents
	agents, err := NewAgents(cfg, llmProvider, telemetry)
	if err != nil {
		return nil, fmt.Errorf("failed to create agents: %w", err)
	}

	// Initialize source providers
	sources, _ := NewSourceProviders(cfg.Sources)

	// Initialize storage
	storage, err := NewStorage(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	o := &Orchestrator{
		config:      cfg,
		logger:      logger,
		telemetry:   telemetry,
		planner:     planner,
		agents:      agents,
		llmProvider: llmProvider,
		sources:     sources,
		storage:     storage,
		processing:  make(map[string]*ProcessingStatus),
		semaphore:   make(chan struct{}, cfg.Agents.MaxConcurrentAgents),
	}

	return o, nil
}

// ProcessThought processes a user thought and returns comprehensive results
func (o *Orchestrator) ProcessThought(ctx context.Context, thought UserThought) (ProcessingResult, error) {
	startTime := time.Now()

	// Generate unique ID if not provided
	if thought.ID == "" {
		thought.ID = uuid.New().String()
	}

	// Initialize processing status
	status := &ProcessingStatus{
		ThoughtID:   thought.ID,
		Status:      "pending",
		Progress:    0.0,
		CreatedAt:   time.Now(),
		LastUpdated: time.Now(),
	}

	o.mu.Lock()
	o.processing[thought.ID] = status
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		delete(o.processing, thought.ID)
		o.mu.Unlock()
	}()

	// Acquire semaphore for concurrency control
	select {
	case o.semaphore <- struct{}{}:
		defer func() { <-o.semaphore }()
	case <-ctx.Done():
		return ProcessingResult{}, ctx.Err()
	}

	// Record processing event
	processingEvent := telemetry.ProcessingEvent{
		ID:          thought.ID,
		UserThought: thought.Content,
		StartTime:   startTime,
	}

	defer func() {
		processingEvent.EndTime = time.Now()
		processingEvent.ProcessingTime = processingEvent.EndTime.Sub(processingEvent.StartTime)
		o.telemetry.RecordProcessingEvent(ctx, processingEvent)
	}()

	o.logger.Printf("Starting processing for thought: %s", thought.ID)
	o.updateStatus(status, "planning", 0.1, "Creating execution plan")

	// Phase 1: Planning
	plan, err := o.planner.Plan(ctx, thought)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Planning failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		return ProcessingResult{}, fmt.Errorf("planning failed: %w", err)
	}

	o.updateStatus(status, "executing", 0.2, "Executing planned tasks")
	status.TotalTasks = len(plan.Tasks)
	status.EstimatedTime = plan.EstimatedTime

	// Phase 2: Execute tasks
	results, err := o.executeTasks(ctx, thought, plan, status)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Execution failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		return ProcessingResult{}, fmt.Errorf("execution failed: %w", err)
	}

	o.updateStatus(status, "synthesizing", 0.8, "Synthesizing results")

	// Phase 3: Synthesis
	result, err := o.synthesizeResults(ctx, thought, results, plan)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Synthesis failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		return ProcessingResult{}, fmt.Errorf("synthesis failed: %w", err)
	}

	// Update processing event with final data
	processingEvent.Success = true
	processingEvent.Cost = result.CostEstimate
	processingEvent.TokensUsed = result.TokensUsed
	processingEvent.AgentsUsed = result.AgentsUsed
	processingEvent.LLMModelsUsed = result.LLMModelsUsed
	processingEvent.SourcesUsed = make([]string, len(result.Sources))
	for i, source := range result.Sources {
		processingEvent.SourcesUsed[i] = source.Type
	}

	// Persistence of result is handled by API server layer after orchestration completes

	o.updateStatus(status, "completed", 1.0, "Processing completed successfully")
	o.logger.Printf("Completed processing for thought: %s in %v", thought.ID, time.Since(startTime))

	return result, nil
}

// executeTasks executes all planned tasks in the correct order
func (o *Orchestrator) executeTasks(ctx context.Context, thought UserThought, plan PlanningResult, status *ProcessingStatus) ([]AgentResult, error) {
	var results []AgentResult
	var mu sync.Mutex

	// Create a map for quick task lookup
	taskMap := make(map[string]AgentTask)
	for _, task := range plan.Tasks {
		taskMap[task.ID] = task
	}

	// Execute tasks in dependency order
	executed := make(map[string]bool)

	for len(executed) < len(plan.Tasks) {
		// Find tasks that are ready to execute (all dependencies satisfied)
		var readyTasks []AgentTask
		for _, task := range plan.Tasks {
			if executed[task.ID] {
				continue
			}

			// Check if all dependencies are satisfied
			ready := true
			for _, depID := range task.DependsOn {
				if !executed[depID] {
					ready = false
					break
				}
			}

			if ready {
				readyTasks = append(readyTasks, task)
			}
		}

		if len(readyTasks) == 0 {
			return nil, fmt.Errorf("circular dependency detected or missing tasks")
		}

		// Execute ready tasks concurrently
		var wg sync.WaitGroup
		errors := make(chan error, len(readyTasks))

		for _, task := range readyTasks {
			wg.Add(1)
			go func(t AgentTask) {
				defer wg.Done()

				// Find the appropriate agent for this task
				agent := o.findBestAgent(t)
				if agent == nil {
					errors <- fmt.Errorf("no suitable agent found for task: %s", t.ID)
					return
				}

				// Execute the task
				result, err := agent.Execute(ctx, t)
				if err != nil {
					errors <- fmt.Errorf("task %s failed: %w", t.ID, err)
					return
				}

				// Record agent event
				agentEvent := telemetry.AgentEvent{
					ID:         result.ID,
					AgentType:  result.AgentType,
					StartTime:  result.CreatedAt,
					EndTime:    result.CreatedAt.Add(result.ProcessingTime),
					Duration:   result.ProcessingTime,
					Success:    result.Success,
					Error:      result.Error,
					Cost:       result.Cost,
					TokensUsed: result.TokensUsed,
					ModelUsed:  result.ModelUsed,
					Confidence: result.Confidence,
				}
				o.telemetry.RecordAgentEvent(ctx, agentEvent)

				// Add result to results slice
				mu.Lock()
				results = append(results, result)
				executed[t.ID] = true
				status.CompletedTasks++
				status.Progress = float64(status.CompletedTasks) / float64(status.TotalTasks)
				status.CurrentTask = t.Description
				status.LastUpdated = time.Now()
				mu.Unlock()

				o.logger.Printf("Completed task: %s (%s) in %v", t.ID, t.Description, result.ProcessingTime)
			}(task)
		}

		wg.Wait()
		close(errors)

		// Check for errors
		for err := range errors {
			return nil, err
		}
	}

	return results, nil
}

// synthesizeResults synthesizes all agent results into a final processing result
func (o *Orchestrator) synthesizeResults(ctx context.Context, thought UserThought, results []AgentResult, plan PlanningResult) (ProcessingResult, error) {
	// Collect all sources and data
	var allSources []Source
	var allData map[string]interface{}
	var totalCost float64
	var totalTokens int64
	var agentsUsed []string
	var modelsUsed []string

	allData = make(map[string]interface{})
	agentSet := make(map[string]bool)
	modelSet := make(map[string]bool)
	var kgNodes []KnowledgeNode
	var kgEdges []KnowledgeEdge

	for _, result := range results {
		// Collect sources
		allSources = append(allSources, result.Sources...)

		// Collect data
		for k, v := range result.Data {
			allData[k] = v
		}

		// Track costs and tokens
		totalCost += result.Cost
		totalTokens += result.TokensUsed

		// Track agents and models
		if !agentSet[result.AgentType] {
			agentsUsed = append(agentsUsed, result.AgentType)
			agentSet[result.AgentType] = true
		}
		if !modelSet[result.ModelUsed] {
			modelsUsed = append(modelsUsed, result.ModelUsed)
			modelSet[result.ModelUsed] = true
		}

		// Capture knowledge graph data if present
		if nodes, ok := result.Data["nodes"].([]KnowledgeNode); ok {
			kgNodes = append(kgNodes, nodes...)
		}
		if edges, ok := result.Data["edges"].([]KnowledgeEdge); ok {
			kgEdges = append(kgEdges, edges...)
		}
	}

	// Use synthesis agent to create final report
	synthesisTask := AgentTask{
		ID:          uuid.New().String(),
		Type:        "synthesis",
		Description: "Synthesize all research and analysis into a comprehensive report",
		Priority:    1,
		Parameters: map[string]interface{}{
			"user_thought": thought.Content,
			"all_data":     allData,
			"sources":      allSources,
			"results":      results,
		},
		Timeout:   o.config.Agents.AgentTimeout,
		CreatedAt: time.Now(),
	}

	synthesisAgent := o.findBestAgent(synthesisTask)
	if synthesisAgent == nil {
		return ProcessingResult{}, fmt.Errorf("no synthesis agent available")
	}

	synthesisResult, err := synthesisAgent.Execute(ctx, synthesisTask)
	if err != nil {
		return ProcessingResult{}, fmt.Errorf("synthesis failed: %w", err)
	}

	// Extract synthesis data
	summary, _ := synthesisResult.Data["summary"].(string)
	detailedReport, _ := synthesisResult.Data["detailed_report"].(string)
	highlights, _ := synthesisResult.Data["highlights"].([]Highlight)
	conflicts, _ := synthesisResult.Data["conflicts"].([]Conflict)
	confidence, _ := synthesisResult.Data["confidence"].(float64)

	// Add synthesis costs
	totalCost += synthesisResult.Cost
	totalTokens += synthesisResult.TokensUsed

	// Create final result
	result := ProcessingResult{
		ID:             thought.ID,
		UserThought:    thought,
		Summary:        summary,
		DetailedReport: detailedReport,
		Sources:        allSources,
		Highlights:     highlights,
		Conflicts:      conflicts,
		Confidence:     confidence,
		ProcessingTime: time.Since(thought.Timestamp),
		CostEstimate:   totalCost,
		TokensUsed:     totalTokens,
		AgentsUsed:     agentsUsed,
		LLMModelsUsed:  modelsUsed,
		CreatedAt:      time.Now(),
	}
	if len(kgNodes) > 0 || len(kgEdges) > 0 {
		if result.Metadata == nil {
			result.Metadata = make(map[string]interface{})
		}
		result.Metadata["knowledge_graph"] = map[string]interface{}{
			"nodes": kgNodes,
			"edges": kgEdges,
		}
	}

	return result, nil
}

// findBestAgent finds the best agent for a given task
func (o *Orchestrator) findBestAgent(task AgentTask) Agent {
	var bestAgent Agent
	var bestConfidence float64

	for _, agent := range o.agents {
		confidence := agent.GetConfidence(task)
		if confidence > bestConfidence {
			bestConfidence = confidence
			bestAgent = agent
		}
	}

	return bestAgent
}

// updateStatus updates the processing status
func (o *Orchestrator) updateStatus(status *ProcessingStatus, newStatus string, progress float64, currentTask string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	status.Status = newStatus
	status.Progress = progress
	status.CurrentTask = currentTask
	status.LastUpdated = time.Now()
}

// GetStatus returns the current status of processing
func (o *Orchestrator) GetStatus(thoughtID string) (ProcessingStatus, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	status, exists := o.processing[thoughtID]
	if !exists {
		return ProcessingStatus{}, fmt.Errorf("thought not found: %s", thoughtID)
	}

	return *status, nil
}

// CancelProcessing cancels ongoing processing
func (o *Orchestrator) CancelProcessing(thoughtID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	status, exists := o.processing[thoughtID]
	if !exists {
		return fmt.Errorf("thought not found: %s", thoughtID)
	}

	status.Status = "cancelled"
	status.LastUpdated = time.Now()

	return nil
}

// GetPerformanceMetrics returns performance metrics
func (o *Orchestrator) GetPerformanceMetrics() map[string]interface{} {
	metrics := o.telemetry.GetMetrics()
	costs := o.telemetry.GetCostSummary()

	return map[string]interface{}{
		"metrics": metrics,
		"costs":   costs,
		"report":  o.telemetry.GetPerformanceReport(),
	}
}
