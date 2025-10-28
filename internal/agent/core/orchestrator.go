package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/capability"
	"github.com/mohammad-safakhou/newser/internal/planner"
	policypkg "github.com/mohammad-safakhou/newser/internal/policy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Orchestrator coordinates all agents and manages the processing pipeline
type Orchestrator struct {
	config      *config.Config
	logger      *log.Logger
	telemetry   *telemetry.Telemetry
	capRegistry *capability.Registry

	// Core components
	planner     *Planner
	agents      map[string]Agent
	llmProvider LLMProvider
	sources     []SourceProvider
	storage     Storage
	planRepo    PlanRepository
	episodes    EpisodeRepository
	templates   ProceduralTemplateRepository
	fairness    *policypkg.FairnessPolicy

	// Processing state
	processing map[string]*ProcessingStatus
	mu         sync.RWMutex

	// Concurrency control
	semaphore chan struct{}
}

// FairnessStats captures the outcome of credibility adjustments.
type FairnessStats struct {
	Bias               float64
	AverageCredibility float64
	LowCredibility     int
	TotalSources       int
}

var orchestratorTracer trace.Tracer = otel.Tracer("newser/internal/agent/orchestrator")

// NewOrchestrator creates a new orchestrator instance
func NewOrchestrator(cfg *config.Config, logger *log.Logger, telemetry *telemetry.Telemetry, registry *capability.Registry, plans PlanRepository, episodes EpisodeRepository, templates ProceduralTemplateRepository) (*Orchestrator, error) {
	// Initialize LLM provider
	llmProvider, err := NewLLMProvider(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Initialize planner
	planner := NewPlanner(cfg, llmProvider, telemetry, registry)

	// Initialize agents
	agents, err := NewAgents(cfg, llmProvider, telemetry, registry)
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

	fairnessPolicy, err := policypkg.NewFairnessPolicy(cfg.Fairness)
	if err != nil {
		return nil, fmt.Errorf("failed to build fairness policy: %w", err)
	}

	o := &Orchestrator{
		config:      cfg,
		logger:      logger,
		telemetry:   telemetry,
		capRegistry: registry,
		planner:     planner,
		agents:      agents,
		llmProvider: llmProvider,
		sources:     sources,
		storage:     storage,
		planRepo:    plans,
		episodes:    episodes,
		templates:   templates,
		fairness:    fairnessPolicy,
		processing:  make(map[string]*ProcessingStatus),
		semaphore:   make(chan struct{}, cfg.Agents.MaxConcurrentAgents),
	}
	if templates != nil {
		o.planner.SetTemplateRepository(templates)
	}

	return o, nil
}

// LLM exposes the orchestrator's underlying LLM provider.
func (o *Orchestrator) LLM() LLMProvider {
	return o.llmProvider
}

// AttachSemanticMemory wires semantic memory lookup into the planner.
func (o *Orchestrator) AttachSemanticMemory(memory SemanticMemory) {
	if o == nil || o.planner == nil {
		return
	}
	o.planner.SetSemanticMemory(memory)
}

// ProcessThought processes a user thought and returns comprehensive results
func (o *Orchestrator) ProcessThought(ctx context.Context, thought UserThought) (ProcessingResult, error) {
	startTime := time.Now()
	ctx, span := orchestratorTracer.Start(ctx, "agent.process_thought",
		trace.WithAttributes(
			attribute.String("thought.id", thought.ID),
			attribute.String("topic.id", thought.TopicID),
			attribute.String("user.id", thought.UserID),
		))
	defer span.End()

	// Generate unique ID if not provided
	if thought.ID == "" {
		thought.ID = uuid.New().String()
		span.SetAttributes(attribute.String("thought.id", thought.ID))
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
	planCtx, planSpan := orchestratorTracer.Start(ctx, "agent.plan")
	plan, err := o.planner.Plan(planCtx, thought)
	if err != nil {
		var approvalErr budget.ErrApprovalRequired
		if errors.As(err, &approvalErr) {
			o.updateStatus(status, "pending_approval", 0.0, err.Error())
			processingEvent.Success = false
			processingEvent.Error = err.Error()
			planSpan.RecordError(err)
			planSpan.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			planSpan.End()
			return ProcessingResult{}, err
		}
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Planning failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		planSpan.RecordError(err)
		planSpan.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		planSpan.End()
		return ProcessingResult{}, fmt.Errorf("planning failed: %w", err)
	}
	span.AddEvent("plan.complete", trace.WithAttributes(
		attribute.Int("plan.task_count", len(plan.Tasks)),
		attribute.Float64("plan.estimated_cost", plan.EstimatedCost),
		attribute.Float64("plan.confidence", plan.Confidence),
	))
	planSpan.SetStatus(codes.Ok, "completed")
	planSpan.End()

	if len(plan.RawJSON) > 0 {
		if err := planner.ValidatePlanDocument(plan.RawJSON); err != nil {
			o.updateStatus(status, "failed", 0.0, "Plan document invalid")
			processingEvent.Success = false
			processingEvent.Error = err.Error()
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return ProcessingResult{}, fmt.Errorf("plan schema validation failed: %w", err)
		}
	}

	if o.planRepo != nil && plan.Graph != nil {
		planID, err := o.planRepo.SavePlanGraph(ctx, thought.ID, plan.Graph, plan.RawJSON)
		if err != nil {
			o.logger.Printf("[ORCH] warn: saving plan graph failed: %v", err)
		} else if plan.Graph.PlanID == "" {
			plan.Graph.PlanID = planID
		}
	}

	o.updateStatus(status, "executing", 0.2, "Executing planned tasks")
	status.TotalTasks = len(plan.Tasks)
	status.EstimatedTime = plan.EstimatedTime

	// Phase 2: Execute tasks
	var monitor *budget.Monitor
	if plan.BudgetConfig != nil && !plan.BudgetConfig.IsZero() {
		monitor = budget.NewMonitor(*plan.BudgetConfig)
	}
	results, traces, err := o.executeTasks(ctx, thought, plan, status, monitor)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Execution failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return ProcessingResult{}, fmt.Errorf("execution failed: %w", err)
	}
	if monitor != nil {
		if err := monitor.CheckTime(); err != nil {
			o.updateStatus(status, "failed", 0.0, err.Error())
			processingEvent.Success = false
			processingEvent.Error = err.Error()
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return ProcessingResult{}, err
		}
	}

	o.updateStatus(status, "synthesizing", 0.8, "Synthesizing results")

	// Phase 3: Synthesis
	synthCtx, synthSpan := orchestratorTracer.Start(ctx, "agent.synthesize")
	result, err := o.synthesizeResults(synthCtx, thought, results, plan, monitor)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Synthesis failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		synthSpan.RecordError(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		synthSpan.SetStatus(codes.Error, err.Error())
		synthSpan.End()
		return ProcessingResult{}, fmt.Errorf("synthesis failed: %w", err)
	}
	synthSpan.SetStatus(codes.Ok, "completed")
	synthSpan.End()

	if o.episodes != nil {
		if err := o.persistEpisode(ctx, thought, plan, traces, result); err != nil {
			o.logger.Printf("episode persistence failed: %v", err)
		}
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
	span.SetAttributes(
		attribute.Float64("run.cost_usd", result.CostEstimate),
		attribute.Int64("run.tokens", result.TokensUsed),
		attribute.Int("run.agent_count", len(result.AgentsUsed)),
	)
	span.SetStatus(codes.Ok, "completed")

	return result, nil
}

// executeTasks executes all planned tasks in the correct order
func (o *Orchestrator) executeTasks(ctx context.Context, thought UserThought, plan PlanningResult, status *ProcessingStatus, monitor *budget.Monitor) ([]AgentResult, []EpisodicStep, error) {
	var (
		results []AgentResult
		traces  []EpisodicStep
		mu      sync.Mutex
	)
	resultsByID := make(map[string]AgentResult)
	executed := make(map[string]bool)

	for len(executed) < len(plan.Tasks) {
		if monitor != nil {
			if err := monitor.CheckTime(); err != nil {
				return nil, nil, err
			}
		}

		var readyTasks []AgentTask
		for _, task := range plan.Tasks {
			if executed[task.ID] {
				continue
			}
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
			return nil, nil, fmt.Errorf("circular dependency detected or missing tasks")
		}

		var wg sync.WaitGroup
		errCh := make(chan error, len(readyTasks))

		for _, task := range readyTasks {
			wg.Add(1)
			go func(t AgentTask) {
				defer wg.Done()

				taskCtx, taskSpan := orchestratorTracer.Start(ctx, "agent.execute",
					trace.WithAttributes(
						attribute.String("task.id", t.ID),
						attribute.String("task.type", t.Type),
						attribute.Int("task.priority", t.Priority),
					))
				defer taskSpan.End()

				if o.capRegistry != nil {
					if _, ok := o.capRegistry.Tool(t.Type); !ok {
						err := fmt.Errorf("no registered ToolCard for agent type: %s", t.Type)
						taskSpan.RecordError(err)
						taskSpan.SetStatus(codes.Error, err.Error())
						errCh <- err
						return
					}
				}

				agent := o.findBestAgent(t)
				if agent == nil {
					err := fmt.Errorf("no suitable agent found for task: %s", t.ID)
					taskSpan.RecordError(err)
					taskSpan.SetStatus(codes.Error, err.Error())
					errCh <- err
					return
				}

				enriched := t
				params := make(map[string]interface{}, len(t.Parameters)+3)
				for k, v := range t.Parameters {
					params[k] = v
				}
				if thought.Context != nil {
					params["context"] = thought.Context
				}
				if thought.Preferences != nil {
					params["preferences"] = thought.Preferences
				}
				var (
					inputs     []AgentResult
					aggSources []Source
				)
				for _, depID := range t.DependsOn {
					mu.Lock()
					if r, ok := resultsByID[depID]; ok {
						inputs = append(inputs, r)
						if len(r.Sources) > 0 {
							aggSources = append(aggSources, r.Sources...)
						}
					}
					mu.Unlock()
				}
				if len(inputs) > 0 {
					params["inputs"] = inputs
				}
				if _, ok := params["sources"]; !ok && len(aggSources) > 0 {
					params["sources"] = DeduplicateSources(aggSources)
				}
				enriched.Parameters = params

				result, err := agent.Execute(taskCtx, enriched)
				if err != nil {
					taskSpan.RecordError(err)
					taskSpan.SetStatus(codes.Error, err.Error())
					errCh <- fmt.Errorf("task %s failed: %w", t.ID, err)
					return
				}

				if monitor != nil {
					if err := monitor.Add(result.Cost, result.TokensUsed); err != nil {
						taskSpan.RecordError(err)
						taskSpan.SetStatus(codes.Error, err.Error())
						returnErr := err
						if plan.BudgetConfig != nil {
							usage := budget.UsageFromMonitor(monitor)
							o.telemetry.RecordBudgetUsage(ctx, usage, *plan.BudgetConfig, true)
							var exceeded budget.ErrExceeded
							if errors.As(err, &exceeded) {
								returnErr = budget.NewBreach(exceeded, usage)
							}
						}
						errCh <- returnErr
						return
					}
					if err := monitor.CheckTime(); err != nil {
						taskSpan.RecordError(err)
						taskSpan.SetStatus(codes.Error, err.Error())
						returnErr := err
						if plan.BudgetConfig != nil {
							usage := budget.UsageFromMonitor(monitor)
							o.telemetry.RecordBudgetUsage(ctx, usage, *plan.BudgetConfig, true)
							var exceeded budget.ErrExceeded
							if errors.As(err, &exceeded) {
								returnErr = budget.NewBreach(exceeded, usage)
							}
						}
						errCh <- returnErr
						return
					}
				}
				taskSpan.SetAttributes(
					attribute.String("agent.type", result.AgentType),
					attribute.Bool("agent.success", result.Success),
					attribute.Float64("agent.cost_usd", result.Cost),
					attribute.Int64("agent.tokens", result.TokensUsed),
					attribute.Float64("agent.confidence", result.Confidence),
				)
				if result.Success {
					taskSpan.SetStatus(codes.Ok, "completed")
				} else {
					if result.Error != "" {
						taskSpan.RecordError(errors.New(result.Error))
					}
					taskSpan.SetStatus(codes.Error, "agent reported failure")
				}

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
				o.telemetry.RecordAgentEvent(taskCtx, agentEvent)

				mu.Lock()
				results = append(results, result)
				executed[t.ID] = true
				resultsByID[t.ID] = result
				trace := EpisodicStep{
					StepIndex:     len(traces),
					Task:          enriched,
					InputSnapshot: enriched.Parameters,
					Prompt:        result.Prompt,
					Result:        result,
					Artifacts:     collectArtifacts(result),
					StartedAt:     result.CreatedAt.Add(-result.ProcessingTime),
					CompletedAt:   result.CreatedAt,
				}
				traces = append(traces, trace)
				status.CompletedTasks++
				status.Progress = float64(status.CompletedTasks) / float64(status.TotalTasks)
				status.CurrentTask = t.Description
				status.LastUpdated = time.Now()
				mu.Unlock()

				o.logger.Printf("Completed task: %s (%s) in %v", t.ID, t.Description, result.ProcessingTime)
			}(task)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			if err != nil {
				return nil, nil, err
			}
		}
	}

	return results, traces, nil
}

// synthesizeResults synthesizes all agent results into a final processing result
func (o *Orchestrator) synthesizeResults(ctx context.Context, thought UserThought, results []AgentResult, plan PlanningResult, monitor *budget.Monitor) (ProcessingResult, error) {
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

	// Find a synthesis result from planned tasks (prefer the last one)
	var synthesisResult *AgentResult
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].AgentType == "synthesis" && results[i].Success {
			r := results[i]
			synthesisResult = &r
			break
		}
	}
	if synthesisResult == nil {
		return ProcessingResult{}, fmt.Errorf("no synthesis result available (planner must include a final synthesis task)")
	}

	// Extract synthesis data
	summary, _ := synthesisResult.Data["summary"].(string)
	detailedReport, _ := synthesisResult.Data["detailed_report"].(string)
	highlights, _ := synthesisResult.Data["highlights"].([]Highlight)
	conflicts, _ := synthesisResult.Data["conflicts"].([]Conflict)
	confidence, _ := synthesisResult.Data["confidence"].(float64)
	// Items for digest
	var items []map[string]interface{}
	if v, ok := synthesisResult.Data["items"].([]map[string]interface{}); ok {
		items = v
	} else if vi, ok := synthesisResult.Data["items"].([]interface{}); ok {
		for _, it := range vi {
			if m, ok := it.(map[string]interface{}); ok {
				items = append(items, m)
			}
		}
	}

	// Include synthesis costs
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
	// Always include knowledge graph in metadata, even if empty
	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{})
	}
	result.Metadata["knowledge_graph"] = map[string]interface{}{
		"nodes": kgNodes,
		"edges": kgEdges,
		"topic": thought.Content,
	}
	var budgetUsage budget.Usage
	var fairnessStats *FairnessStats
	if monitor != nil {
		budgetUsage = budget.UsageFromMonitor(monitor)
		result.Metadata["budget_usage"] = budget.SerializeUsage(budgetUsage)
		if plan.BudgetEstimate != nil && plan.BudgetConfig != nil {
			result.Metadata["budget_report"] = budget.BuildReport(*plan.BudgetConfig, *plan.BudgetEstimate, budgetUsage)
		}
	}
	if monitor == nil {
		// still include estimate for consumers even when no monitor was instantiated.
		if plan.BudgetEstimate != nil && plan.BudgetConfig != nil {
			result.Metadata["budget_report"] = budget.BuildReport(*plan.BudgetConfig, *plan.BudgetEstimate, budgetUsage)
		}
	}
	if plan.BudgetConfig != nil {
		o.telemetry.RecordBudgetUsage(ctx, budgetUsage, *plan.BudgetConfig, false)
	}
	sourceByID := make(map[string]*Source, len(allSources))
	if len(allSources) > 0 {
		ensureSourceIDs(&allSources)
		for i := range allSources {
			if allSources[i].ID != "" {
				sourceByID[allSources[i].ID] = &allSources[i]
			}
		}
		if stats := o.applyFairness(&allSources, thought.Preferences); stats != nil {
			fairnessStats = stats
		}
	}

	if len(items) > 0 {
		sourceLookup := buildSourceLookup(allSources)
		var filtered []map[string]interface{}
		for _, item := range items {
			if normSources, sourceIDs := normalizeItemSources(item, sourceLookup); len(sourceIDs) > 0 {
				sort.SliceStable(normSources, func(a, b int) bool {
					da, _ := normSources[a]["domain"].(string)
					db, _ := normSources[b]["domain"].(string)
					wa := isWikipediaDomain(da)
					wb := isWikipediaDomain(db)
					if wa == wb {
						return da < db
					}
					return !wa && wb
				})
				arr := make([]interface{}, len(normSources))
				for idx := range normSources {
					arr[idx] = normSources[idx]
				}
				item["sources"] = arr
				item["source_ids"] = sourceIDs
				filtered = append(filtered, item)
			} else {
				title := ""
				if t, ok := item["title"].(string); ok {
					title = strings.TrimSpace(t)
				}
				if title == "" {
					title = "(unknown)"
				}
				o.logger.Printf("dropping digest item %q due to missing verified sources", title)
			}
		}
		if len(filtered) == 0 {
			delete(result.Metadata, "items")
			delete(result.Metadata, "digest_stats")
		} else {
			items = filtered
			if ev := buildEvidence(items, sourceByID); len(ev) > 0 {
				result.Evidence = ev
			}
			arr := make([]interface{}, len(items))
			for i := range items {
				arr[i] = items[i]
			}
			result.Metadata["items"] = arr
			stats := map[string]interface{}{}
			stats["count"] = len(items)
			cat := map[string]int{}
			var earliest, latest time.Time
			domains := map[string]int{}
			now := time.Now()
			for idx, it := range items {
				if c, ok := it["category"].(string); ok && c != "" {
					cat[c]++
				}
				if pubs, ok := it["published_at"].(string); ok && pubs != "" {
					if t, err := time.Parse(time.RFC3339, pubs); err == nil {
						if earliest.IsZero() || t.Before(earliest) {
							earliest = t
						}
						if latest.IsZero() || t.After(latest) {
							latest = t
						}
					}
				}
				if _, ok := it["seen_at"]; !ok {
					items[idx]["seen_at"] = now.Format(time.RFC3339)
				}
				if srcs, ok := it["sources"].([]interface{}); ok {
					for _, s := range srcs {
						if sm, ok := s.(map[string]interface{}); ok {
							if d, ok := sm["domain"].(string); ok && d != "" {
								domains[d]++
							}
						}
					}
				}
			}
			stats["categories"] = cat
			if !earliest.IsZero() && !latest.IsZero() {
				stats["span_hours"] = latest.Sub(earliest).Hours()
				stats["earliest"] = earliest.Format(time.RFC3339)
				stats["latest"] = latest.Format(time.RFC3339)
			}
			type kv struct {
				k string
				v int
			}
			var kvs []kv
			for k, v := range domains {
				kvs = append(kvs, kv{k, v})
			}
			sort.Slice(kvs, func(i, j int) bool {
				if kvs[i].v == kvs[j].v {
					return kvs[i].k < kvs[j].k
				}
				return kvs[i].v > kvs[j].v
			})
			top := []string{}
			for i := 0; i < len(kvs) && i < 5; i++ {
				top = append(top, kvs[i].k)
			}
			stats["top_domains"] = top
			result.Metadata["digest_stats"] = stats
		}
	} else {
		delete(result.Metadata, "items")
		delete(result.Metadata, "digest_stats")
	}

	if fairnessStats != nil {
		result.Metadata["fairness"] = map[string]interface{}{
			"bias":                  fairnessStats.Bias,
			"avg_credibility":       fairnessStats.AverageCredibility,
			"low_credibility_count": fairnessStats.LowCredibility,
			"min_credibility":       o.fairness.MinCredibility,
		}
		if o.telemetry != nil {
			o.telemetry.RecordFairness(ctx, telemetry.FairnessMetrics{
				Bias:               fairnessStats.Bias,
				AverageCredibility: fairnessStats.AverageCredibility,
				LowCredibility:     fairnessStats.LowCredibility,
				TotalSources:       fairnessStats.TotalSources,
			})
		}
	}

	return result, nil
}

func (o *Orchestrator) persistEpisode(ctx context.Context, thought UserThought, plan PlanningResult, traces []EpisodicStep, result ProcessingResult) error {
	if o.episodes == nil {
		return nil
	}
	if thought.ID == "" || thought.TopicID == "" || thought.UserID == "" {
		return nil
	}
	snapshot := EpisodicSnapshot{
		RunID:        thought.ID,
		TopicID:      thought.TopicID,
		UserID:       thought.UserID,
		Thought:      thought,
		PlanDocument: plan.Graph,
		PlanRaw:      plan.RawJSON,
		PlanPrompt:   plan.Prompt,
		Result:       result,
	}
	if len(traces) > 0 {
		snapshot.Steps = make([]EpisodicStep, len(traces))
		copy(snapshot.Steps, traces)
	}
	if len(snapshot.PlanRaw) == 0 && plan.Graph != nil {
		if b, err := json.Marshal(plan.Graph); err == nil {
			snapshot.PlanRaw = b
		}
	}
	return o.episodes.SaveEpisode(ctx, snapshot)
}

func collectArtifacts(res AgentResult) []map[string]interface{} {
	if res.Data == nil {
		return nil
	}
	raw, ok := res.Data["artifacts"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []map[string]interface{}:
		return v
	case []interface{}:
		var out []map[string]interface{}
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func (o *Orchestrator) applyFairness(sources *[]Source, preferences map[string]interface{}) *FairnessStats {
	if o == nil || o.fairness == nil || !o.fairness.Enabled {
		return nil
	}
	if sources == nil || len(*sources) == 0 {
		return nil
	}
	bias := o.fairness.BiasFromPreferences(preferences)
	list := *sources
	var total float64
	low := 0
	for i := range list {
		domain := normalizeSourceURL(list[i].URL)
		adj, _ := o.fairness.Adjust(domain, list[i].Credibility, bias)
		list[i].Credibility = adj
		total += adj
		if adj < o.fairness.MinCredibility {
			low++
		}
	}
	avg := 0.0
	if len(list) > 0 {
		avg = total / float64(len(list))
	}
	return &FairnessStats{Bias: bias, AverageCredibility: avg, LowCredibility: low, TotalSources: len(list)}
}

func ensureSourceIDs(sources *[]Source) {
	if sources == nil {
		return
	}
	for i := range *sources {
		if (*sources)[i].ID == "" {
			(*sources)[i].ID = uuid.NewString()
		}
	}
}

func buildSourceLookup(sources []Source) map[string]*Source {
	lookup := make(map[string]*Source, len(sources))
	for i := range sources {
		norm := normalizeSourceURL(sources[i].URL)
		if norm == "" {
			continue
		}
		lookup[norm] = &sources[i]
		if trimmed := strings.TrimSuffix(norm, "/"); trimmed != norm {
			lookup[trimmed] = &sources[i]
		}
	}
	return lookup
}

func normalizeItemSources(item map[string]interface{}, lookup map[string]*Source) ([]map[string]interface{}, []string) {
	if len(lookup) == 0 {
		return nil, nil
	}
	var rawSources []map[string]interface{}
	switch v := item["sources"].(type) {
	case []map[string]interface{}:
		rawSources = v
	case []interface{}:
		for _, s := range v {
			if sm, ok := s.(map[string]interface{}); ok {
				rawSources = append(rawSources, sm)
			}
		}
	default:
		return nil, nil
	}
	if len(rawSources) == 0 {
		return nil, nil
	}
	var out []map[string]interface{}
	var ids []string
	seen := make(map[string]struct{})
	for _, sm := range rawSources {
		if sm == nil {
			continue
		}
		urlVal, _ := sm["url"].(string)
		urlVal = strings.TrimSpace(urlVal)
		if urlVal == "" {
			continue
		}
		norm := normalizeSourceURL(urlVal)
		src, ok := lookup[norm]
		if !ok {
			if alt := strings.TrimSuffix(norm, "/"); alt != norm {
				src, ok = lookup[alt]
			}
		}
		if !ok {
			continue
		}
		m := make(map[string]interface{}, len(sm)+3)
		for k, v := range sm {
			m[k] = v
		}
		domain := toDomain(urlVal)
		if m["domain"] == nil && domain != "" {
			m["domain"] = domain
		}
		if m["archived_url"] == nil || strings.TrimSpace(fmt.Sprint(m["archived_url"])) == "" {
			if isPaywalledDomain(domain) {
				m["archived_url"] = archiveHint(urlVal)
			}
		}
		m["url"] = urlVal
		m["id"] = src.ID
		snippet := trimSnippet(src.Summary)
		if snippet == "" {
			snippet = trimSnippet(src.Content)
		}
		if snippet != "" {
			m["snippet"] = snippet
		} else {
			delete(m, "snippet")
		}
		out = append(out, m)
		if _, exists := seen[src.ID]; !exists {
			ids = append(ids, src.ID)
			seen[src.ID] = struct{}{}
		}
	}
	return out, ids
}

func normalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSuffix(strings.ToLower(raw), "/")
	}
	u.Fragment = ""
	if u.Scheme == "" {
		u.Scheme = "https"
	} else {
		u.Scheme = strings.ToLower(u.Scheme)
	}
	u.Host = strings.ToLower(u.Host)
	if len(u.Path) > 1 {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

func trimSnippet(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const maxRunes = 280
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	trimmed := strings.TrimSpace(string(runes[:maxRunes]))
	if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") {
		return trimmed
	}
	return trimmed + "…"
}

// toDomain extracts the hostname from a URL string.
func toDomain(u string) string {
	if u == "" {
		return ""
	}
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	// strip port
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

func isWikipediaDomain(d string) bool { return strings.Contains(d, "wikipedia.org") }

func isPaywalledDomain(d string) bool {
	if d == "" {
		return false
	}
	// heuristic list; extend as needed
	pay := []string{
		"wsj.com", "ft.com", "financialtimes.com", "bloomberg.com", "economist.com",
		"nytimes.com", "washingtonpost.com", "theatlantic.com", "businessinsider.com",
		"telegraph.co.uk", "ftadviser.com", "foreignaffairs.com",
	}
	for _, p := range pay {
		if strings.HasSuffix(d, p) {
			return true
		}
	}
	return false
}

func archiveHint(u string) string {
	if u == "" {
		return ""
	}
	// do not fetch; provide archive.today runner URL as a hint
	return "https://archive.today/?run=1&url=" + u
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
	// Derived summaries
	summaries := map[string]interface{}{}
	if metrics.ResearchRuns > 0 {
		summaries["research"] = map[string]interface{}{
			"runs":               metrics.ResearchRuns,
			"avg_pages":          float64(metrics.ResearchPagesFetchedTotal) / float64(metrics.ResearchRuns),
			"avg_sources":        float64(metrics.ResearchSourcesTotal) / float64(metrics.ResearchRuns),
			"avg_unique_domains": float64(metrics.ResearchUniqueDomainsTotal) / float64(metrics.ResearchRuns),
			"avg_sources_per_page": func() float64 {
				if metrics.ResearchPagesFetchedTotal == 0 {
					return 0
				}
				return float64(metrics.ResearchSourcesTotal) / float64(metrics.ResearchPagesFetchedTotal)
			}(),
		}
	}
	if metrics.AnalysisRuns > 0 {
		summaries["analysis"] = map[string]interface{}{
			"runs":                   metrics.AnalysisRuns,
			"avg_sources_considered": float64(metrics.AnalysisSourcesConsideredTotal) / float64(metrics.AnalysisRuns),
			"avg_min_credibility":    metrics.AnalysisMinCredibilityTotal / float64(metrics.AnalysisRuns),
		}
	}
	if metrics.KGRuns > 0 {
		summaries["knowledge_graph"] = map[string]interface{}{
			"runs":      metrics.KGRuns,
			"avg_nodes": float64(metrics.KGNodesTotal) / float64(metrics.KGRuns),
			"avg_edges": float64(metrics.KGEdgesTotal) / float64(metrics.KGRuns),
		}
	}
	if metrics.ConflictRuns > 0 {
		summaries["conflict_detection"] = map[string]interface{}{
			"runs":                        metrics.ConflictRuns,
			"avg_conflicts":               float64(metrics.ConflictCountTotal) / float64(metrics.ConflictRuns),
			"avg_contradictory_threshold": metrics.ContradictoryThresholdsTotal / float64(metrics.ConflictRuns),
		}
	}
	return map[string]interface{}{
		"metrics":   metrics,
		"costs":     costs,
		"summaries": summaries,
		"report":    o.telemetry.GetPerformanceReport(),
	}
}
