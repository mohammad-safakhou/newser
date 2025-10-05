package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/capability"
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

	// Processing state
	processing map[string]*ProcessingStatus
	mu         sync.RWMutex

	// Concurrency control
	semaphore chan struct{}
}

// NewOrchestrator creates a new orchestrator instance
func NewOrchestrator(cfg *config.Config, logger *log.Logger, telemetry *telemetry.Telemetry, registry *capability.Registry, plans PlanRepository, episodes EpisodeRepository) (*Orchestrator, error) {
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
		var approvalErr budget.ErrApprovalRequired
		if errors.As(err, &approvalErr) {
			o.updateStatus(status, "pending_approval", 0.0, err.Error())
			processingEvent.Success = false
			processingEvent.Error = err.Error()
			return ProcessingResult{}, err
		}
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Planning failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		return ProcessingResult{}, fmt.Errorf("planning failed: %w", err)
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
		return ProcessingResult{}, fmt.Errorf("execution failed: %w", err)
	}
	if monitor != nil {
		if err := monitor.CheckTime(); err != nil {
			o.updateStatus(status, "failed", 0.0, err.Error())
			processingEvent.Success = false
			processingEvent.Error = err.Error()
			return ProcessingResult{}, err
		}
	}

	o.updateStatus(status, "synthesizing", 0.8, "Synthesizing results")

	// Phase 3: Synthesis
	result, err := o.synthesizeResults(ctx, thought, results, plan, monitor)
	if err != nil {
		o.updateStatus(status, "failed", 0.0, fmt.Sprintf("Synthesis failed: %v", err))
		processingEvent.Success = false
		processingEvent.Error = err.Error()
		return ProcessingResult{}, fmt.Errorf("synthesis failed: %w", err)
	}

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

				agent := o.findBestAgent(t)
				if agent == nil {
					errCh <- fmt.Errorf("no suitable agent found for task: %s", t.ID)
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

				result, err := agent.Execute(ctx, enriched)
				if err != nil {
					errCh <- fmt.Errorf("task %s failed: %w", t.ID, err)
					return
				}

				if monitor != nil {
					if err := monitor.Add(result.Cost, result.TokensUsed); err != nil {
						errCh <- err
						return
					}
					if err := monitor.CheckTime(); err != nil {
						errCh <- err
						return
					}
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
				o.telemetry.RecordAgentEvent(ctx, agentEvent)

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
	if monitor != nil {
		costUsed, tokensUsed, elapsed := monitor.Usage()
		budgetMeta := map[string]interface{}{
			"cost_used":   costUsed,
			"tokens_used": tokensUsed,
			"elapsed":     elapsed.String(),
		}
		cfg := monitor.Config()
		if cfg.MaxCost != nil {
			budgetMeta["max_cost"] = *cfg.MaxCost
		}
		if cfg.MaxTokens != nil {
			budgetMeta["max_tokens"] = *cfg.MaxTokens
		}
		if cfg.MaxTimeSeconds != nil {
			budgetMeta["max_time_seconds"] = *cfg.MaxTimeSeconds
		}
		if cfg.ApprovalThreshold != nil {
			budgetMeta["approval_threshold"] = *cfg.ApprovalThreshold
		}
		budgetMeta["require_approval"] = cfg.RequireApproval
		if len(cfg.Metadata) > 0 {
			budgetMeta["metadata"] = cfg.Metadata
		}
		result.Metadata["budget_usage"] = budgetMeta
	}
	if len(items) > 0 {
		// Normalize item sources: add domain if missing, de-prioritize Wikipedia, and attach archived_url for known paywalled domains
		for i := range items {
			if srcs, ok := items[i]["sources"].([]interface{}); ok && len(srcs) > 0 {
				// normalize each source map
				var norm []map[string]interface{}
				for _, s := range srcs {
					if sm, ok := s.(map[string]interface{}); ok {
						u, _ := sm["url"].(string)
						d := toDomain(u)
						if sm["domain"] == nil && d != "" {
							sm["domain"] = d
						}
						if sm["archived_url"] == nil || sm["archived_url"] == "" {
							if isPaywalledDomain(d) {
								sm["archived_url"] = archiveHint(u)
							}
						}
						norm = append(norm, sm)
					}
				}
				// reorder: non-wikipedia first
				sort.SliceStable(norm, func(a, b int) bool {
					da, _ := norm[a]["domain"].(string)
					db, _ := norm[b]["domain"].(string)
					wa := isWikipediaDomain(da)
					wb := isWikipediaDomain(db)
					if wa == wb {
						return da < db
					}
					return !wa && wb
				})
				// write back as []interface{}
				arr := make([]interface{}, len(norm))
				for k := range norm {
					arr[k] = norm[k]
				}
				items[i]["sources"] = arr
			}
		}
		result.Metadata["items"] = items
		// Compute digest stats
		stats := map[string]interface{}{}
		stats["count"] = len(items)
		// categories
		cat := map[string]int{}
		var earliest, latest time.Time
		domains := map[string]int{}
		now := time.Now()
		for i, it := range items {
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
				items[i]["seen_at"] = now.Format(time.RFC3339)
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
		// top domains (first 5)
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
