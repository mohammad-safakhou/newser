package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"github.com/mohammad-safakhou/newser/internal/capability"
	plannerv1 "github.com/mohammad-safakhou/newser/internal/planner"
	policypkg "github.com/mohammad-safakhou/newser/internal/policy"
)

// Planner creates intelligent execution plans for user thoughts
type Planner struct {
	config      *config.Config
	llmProvider LLMProvider
	telemetry   *telemetry.Telemetry
	logger      *log.Logger
	registry    *capability.Registry
}

// NewPlanner creates a new planner instance
func NewPlanner(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry, registry *capability.Registry) *Planner {
	return &Planner{
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[PLANNER] ", log.LstdFlags),
		registry:    registry,
	}
}

// Plan creates an execution plan for a user thought
func (p *Planner) Plan(ctx context.Context, thought UserThought) (PlanningResult, error) {
	startTime := time.Now()

	// Create planning prompt with schema guidance
	prompt := p.createPlanningPrompt(thought)

	// Get the planning model
	model := p.config.LLM.Routing.Planning

	// Generate plan using LLM
	response, err := p.llmProvider.Generate(ctx, prompt, model, nil)
	if err != nil {
		return PlanningResult{}, fmt.Errorf("failed to generate plan: %w", err)
	}

	// Parse the response
	plan, err := p.parsePlanningResponse(response)
	if err != nil {
		return PlanningResult{}, fmt.Errorf("failed to parse planning response: %w", err)
	}

	// Validate the plan
	if err := p.ValidatePlan(plan); err != nil {
		return PlanningResult{}, fmt.Errorf("plan validation failed: %w", err)
	}

	applyBudgetToPlan(&plan, thought.Budget, p.registry)
	if plan.BudgetApprovalRequired {
		threshold := 0.0
		if plan.BudgetConfig != nil && plan.BudgetConfig.ApprovalThreshold != nil {
			threshold = *plan.BudgetConfig.ApprovalThreshold
		}
		return PlanningResult{}, budget.ErrApprovalRequired{EstimatedCost: plan.EstimatedCost, Threshold: threshold}
	}

	// Normalize dependencies: final synthesis depends on all prior tasks; KG depends on research/analysis
	if len(plan.Tasks) > 0 {
		// final synthesis
		synthIndex := -1
		for i := range plan.Tasks {
			if plan.Tasks[i].Type == "synthesis" {
				synthIndex = i
			}
		}
		if synthIndex >= 0 {
			var others []string
			for i := range plan.Tasks {
				if i != synthIndex {
					others = append(others, plan.Tasks[i].ID)
				}
			}
			deps := append([]string{}, plan.Tasks[synthIndex].DependsOn...)
			have := map[string]bool{}
			for _, d := range deps {
				have[d] = true
			}
			for _, id := range others {
				if !have[id] {
					deps = append(deps, id)
				}
			}
			plan.Tasks[synthIndex].DependsOn = deps
		}
		// KG depends on research/analysis
		var ra []string
		for i := range plan.Tasks {
			if plan.Tasks[i].Type == "research" || plan.Tasks[i].Type == "analysis" {
				ra = append(ra, plan.Tasks[i].ID)
			}
		}
		for i := range plan.Tasks {
			if plan.Tasks[i].Type != "knowledge_graph" {
				continue
			}
			deps := append([]string{}, plan.Tasks[i].DependsOn...)
			have := map[string]bool{}
			for _, d := range deps {
				have[d] = true
			}
			for _, id := range ra {
				if !have[id] {
					deps = append(deps, id)
				}
			}
			plan.Tasks[i].DependsOn = deps
		}
	}

	if thought.Policy != nil {
		cloned := thought.Policy.Clone()
		attachTemporalPolicy(&plan, &cloned)
	}

	processingTime := time.Since(startTime)
	p.logger.Printf("Planning completed in %v with %d tasks", processingTime, len(plan.Tasks))

	return plan, nil
}

func applyBudgetToPlan(plan *PlanningResult, cfg *budget.Config, reg *capability.Registry) {
	if plan == nil {
		return
	}
	populatePlanEstimates(plan, reg)
	plan.BudgetConfig = nil
	plan.BudgetApprovalRequired = false
	plan.Budget = nil
	if plan.Graph != nil {
		plan.Graph.Budget = nil
	}
	if cfg == nil {
		return
	}
	clone := cfg.Clone()
	if clone.IsZero() {
		return
	}
	plan.BudgetConfig = &clone
	if clone.RequireApproval {
		plan.BudgetApprovalRequired = true
	}
	var pb plannerv1.PlanBudget
	haveBudget := false
	if clone.MaxCost != nil {
		pb.MaxCost = *clone.MaxCost
		haveBudget = true
	}
	if clone.MaxTimeSeconds != nil {
		pb.MaxTime = (time.Duration(*clone.MaxTimeSeconds) * time.Second).String()
		haveBudget = true
	}
	if clone.MaxTokens != nil {
		pb.MaxTokens = *clone.MaxTokens
		haveBudget = true
	}
	if haveBudget {
		plan.Budget = &pb
		if plan.Graph != nil {
			copyBudget := pb
			plan.Graph.Budget = &copyBudget
		}
	}
	if plan.Graph != nil {
		if plan.Graph.Metadata == nil {
			plan.Graph.Metadata = make(map[string]interface{})
		}
		plan.Graph.Metadata["budget_source"] = "topic"
	}
	if clone.ApprovalThreshold != nil && plan.EstimatedCost > 0 && plan.EstimatedCost > *clone.ApprovalThreshold {
		plan.BudgetApprovalRequired = true
	}
	if plan.Graph != nil {
		if data, err := json.Marshal(plan.Graph); err == nil {
			plan.RawJSON = data
		}
	}
}

func populatePlanEstimates(plan *PlanningResult, reg *capability.Registry) {
	if plan == nil || reg == nil {
		return
	}
	var totalCost float64
	for _, task := range plan.Tasks {
		if tc, ok := reg.Tool(task.Type); ok {
			totalCost += tc.CostEstimate
		}
	}
	if totalCost <= 0 {
		return
	}
	plan.EstimatedCost = totalCost
	if plan.Estimates == nil {
		plan.Estimates = &plannerv1.PlanEstimates{}
	}
	plan.Estimates.TotalCost = totalCost
	if plan.Graph != nil {
		if plan.Graph.Estimates == nil {
			plan.Graph.Estimates = &plannerv1.PlanEstimates{}
		}
		plan.Graph.Estimates.TotalCost = totalCost
	}
}

func attachTemporalPolicy(plan *PlanningResult, pol *policypkg.UpdatePolicy) {
	if plan == nil || pol == nil {
		return
	}
	clone := pol.Clone()
	plan.TemporalPolicy = &clone
	changed := applyTemporalPolicyHints(plan, clone)
	if plan.Graph != nil {
		if plan.Graph.Metadata == nil {
			plan.Graph.Metadata = make(map[string]interface{})
		}
		plan.Graph.Metadata["temporal_policy"] = temporalPolicyMetadata(clone)
		if updated, err := json.Marshal(plan.Graph); err == nil {
			plan.RawJSON = updated
		}
	} else if changed {
		// no graph to serialize, but ensure RawJSON reflects task hints if original JSON existed
		if len(plan.RawJSON) > 0 {
			if updated, err := json.Marshal(plan); err == nil {
				plan.RawJSON = updated
			}
		}
	}
}

func temporalPolicyMetadata(pol policypkg.UpdatePolicy) map[string]interface{} {
	meta := map[string]interface{}{
		"repeat_mode": string(pol.RepeatMode),
	}
	if pol.RefreshInterval > 0 {
		meta["refresh_interval"] = pol.RefreshInterval.String()
	}
	if pol.DedupWindow > 0 {
		meta["dedup_window"] = pol.DedupWindow.String()
	}
	if pol.FreshnessThreshold > 0 {
		meta["freshness_threshold"] = pol.FreshnessThreshold.String()
	}
	if pol.Metadata != nil && len(pol.Metadata) > 0 {
		metaCopy := make(map[string]interface{}, len(pol.Metadata))
		for k, v := range pol.Metadata {
			metaCopy[k] = v
		}
		meta["metadata"] = metaCopy
	}
	return meta
}

func applyTemporalPolicyHints(plan *PlanningResult, pol policypkg.UpdatePolicy) bool {
	if plan == nil {
		return false
	}
	changed := false

	annotate := func(params map[string]interface{}) (map[string]interface{}, bool) {
		if params == nil {
			params = make(map[string]interface{})
		}
		mutated := false

		setDuration := func(key string, d time.Duration) {
			if d > 0 {
				val := int64(d / time.Second)
				if current, ok := params[key]; !ok || current != val {
					params[key] = val
					mutated = true
				}
			} else if _, ok := params[key]; ok {
				delete(params, key)
				mutated = true
			}
		}

		setDuration("dedup_window_seconds", pol.DedupWindow)
		setDuration("refresh_interval_seconds", pol.RefreshInterval)
		setDuration("freshness_threshold_seconds", pol.FreshnessThreshold)

		if pol.RepeatMode == policypkg.RepeatModeManual {
			if current, ok := params["manual_repeat_mode"]; !ok || current != true {
				params["manual_repeat_mode"] = true
				mutated = true
			}
		} else if _, ok := params["manual_repeat_mode"]; ok {
			delete(params, "manual_repeat_mode")
			mutated = true
		}

		return params, mutated
	}

	for i := range plan.Tasks {
		task := &plan.Tasks[i]
		var doAnnotate bool
		switch task.Type {
		case "research":
			doAnnotate = pol.DedupWindow > 0 || pol.RefreshInterval > 0
		case "analysis":
			doAnnotate = pol.FreshnessThreshold > 0
		case "synthesis":
			doAnnotate = pol.RepeatMode == policypkg.RepeatModeManual
		}
		if !doAnnotate {
			continue
		}
		params, mutated := annotate(task.Parameters)
		if mutated || task.Parameters == nil {
			task.Parameters = params
			changed = true
		}
	}

	if plan.Graph != nil {
		for i := range plan.Graph.Tasks {
			task := &plan.Graph.Tasks[i]
			var doAnnotate bool
			switch task.Type {
			case "research":
				doAnnotate = pol.DedupWindow > 0 || pol.RefreshInterval > 0
			case "analysis":
				doAnnotate = pol.FreshnessThreshold > 0
			case "synthesis":
				doAnnotate = pol.RepeatMode == policypkg.RepeatModeManual
			}
			if !doAnnotate {
				continue
			}
			params, mutated := annotate(task.Parameters)
			if mutated || task.Parameters == nil {
				task.Parameters = params
				changed = true
			}
		}
	}

	return changed
}

// createPlanningPrompt creates a comprehensive prompt for planning
func (p *Planner) createPlanningPrompt(thought UserThought) string {
	// Prior context block
	ctxBlock := ""
	if thought.Context != nil {
		if v, ok := thought.Context["last_run_time"].(string); ok && v != "" {
			ctxBlock += fmt.Sprintf("- Last run time: %s\n", v)
		}
		if v, ok := thought.Context["prev_summary"].(string); ok && v != "" {
			if len(v) > 800 {
				v = v[:800] + "..."
			}
			ctxBlock += fmt.Sprintf("- Previous summary: %s\n", v)
		}
		if arr, ok := thought.Context["known_urls"].([]string); ok && len(arr) > 0 {
			ctxBlock += fmt.Sprintf("- Known URLs (avoid duplicates): %d\n", len(arr))
		}
		if kg, ok := thought.Context["knowledge_graph"].(map[string]interface{}); ok {
			if nodes, ok := kg["nodes"].([]interface{}); ok {
				ctxBlock += fmt.Sprintf("- Existing KG nodes: %d\n", len(nodes))
			}
			if edges, ok := kg["edges"].([]interface{}); ok {
				ctxBlock += fmt.Sprintf("- Existing KG edges: %d\n", len(edges))
			}
		}
	}
	if ctxBlock != "" {
		ctxBlock = "\nPRIOR CONTEXT:\n" + ctxBlock
	}

	// Temporal policy guidance for the planner
	policyBlock := ""
	if thought.Policy != nil {
		policyBlock = "\nTEMPORAL POLICY GUIDANCE:\n"
		policyBlock += fmt.Sprintf("- Repeat mode: %s\n", thought.Policy.RepeatMode)
		if thought.Policy.RefreshInterval > 0 {
			policyBlock += fmt.Sprintf("- Refresh interval between runs: %s\n", thought.Policy.RefreshInterval)
		}
		if thought.Policy.DedupWindow > 0 {
			policyBlock += fmt.Sprintf("- Deduplicate content seen within: %s\n", thought.Policy.DedupWindow)
		}
		if thought.Policy.FreshnessThreshold > 0 {
			policyBlock += fmt.Sprintf("- Treat coverage as stale after: %s\n", thought.Policy.FreshnessThreshold)
		}
		if len(thought.Policy.Metadata) > 0 {
			if b, err := json.MarshalIndent(thought.Policy.Metadata, "", "  "); err == nil {
				policyBlock += "- Additional policy metadata:\n" + string(b) + "\n"
			}
		}
	}

	// Preferences and objectives
	prefBlock := ""
	if thought.Preferences != nil && len(thought.Preferences) > 0 {
		b, _ := json.MarshalIndent(thought.Preferences, "", "  ")
		prefBlock = "\nPREFERENCES JSON:\n" + string(b)
		if s, ok := thought.Preferences["context_summary"].(string); ok && s != "" {
			if len(s) > 600 {
				s = s[:600] + "..."
			}
			prefBlock += "\nCONVERSATION SUMMARY:\n" + s
		}
		if arr, ok := thought.Preferences["objectives"].([]string); ok && len(arr) > 0 {
			prefBlock += "\nOBJECTIVES:\n"
			for _, o := range arr {
				prefBlock += "- " + o + "\n"
			}
		}
	}

	caps := CapabilitiesDoc(p.config)
	return fmt.Sprintf(`ROLE: Senior news intelligence planner focused on accuracy and usefulness. Ignore money/time costs; minimize tasks while maximizing quality.

USER THOUGHT / TOPIC:
%s%s%s%s

AVAILABLE AGENTS:
- research: gather information from sources
- analysis: evaluate relevance, credibility, importance
- synthesis: produce coherent final report (MUST be last)
- conflict_detection: find and resolve conflicting claims
- highlight_management: extract key highlights
- knowledge_graph: extract entities/relations (always include; can be light)

AGENT PARAMETER HINTS (map from preferences when available):
%s

SCHEMA RULES (STRICT JSON, NO commentary):
- Top-level keys: version, tasks, execution_order, estimates (optional), budget (optional), edges (optional), confidence (optional).
- version MUST be semantic (e.g. "v1").
- tasks MUST be an array. Each task requires id (string), type (allowed agent type), description, priority (0-5), parameters (object), depends_on (array), timeout (Go duration string), estimated_cost (number), estimated_time (duration string). Include knowledge_graph and final synthesis tasks.
- edges optional: {"from": task_id, "to": task_id, "kind": "control"|"data"}.
- execution_order is array of task ids in run order.
- estimates {"total_cost": number, "total_time": "duration"}. budget {"max_cost": number, "max_time": "duration"} when constraints exist.
- confidence is number between 0 and 1.
- Output must be valid JSON; no markdown, explanations, or trailing text.

VALID OUTPUT EXAMPLE (abridged):
{
  "version": "v1",
  "tasks": [
    {"id": "t1", "type": "research", "description": "Collect primary sources", "priority": 1, "parameters": {"query": ""}, "depends_on": [], "timeout": "10m", "estimated_cost": 1.0, "estimated_time": "5m"},
    {"id": "t2", "type": "analysis", "description": "Analyse findings", "priority": 1, "parameters": {}, "depends_on": ["t1"], "timeout": "8m", "estimated_cost": 0.8, "estimated_time": "4m"},
    {"id": "t3", "type": "knowledge_graph", "description": "Build graph", "priority": 1, "parameters": {}, "depends_on": ["t1","t2"], "timeout": "6m", "estimated_cost": 0.5, "estimated_time": "3m"},
    {"id": "t4", "type": "synthesis", "description": "Produce executive brief", "priority": 1, "parameters": {}, "depends_on": ["t1","t2","t3"], "timeout": "12m", "estimated_cost": 1.2, "estimated_time": "7m"}
  ],
  "execution_order": ["t1","t2","t3","t4"],
  "estimates": {"total_cost": 3.5, "total_time": "19m"},
  "budget": {"max_cost": 5, "max_time": "30m"},
  "confidence": 0.85
}

PLANNING PRINCIPLES:
1) Create the fewest, highest-impact tasks.
2) Honour dependencies (research -> analysis -> downstream tasks).
3) Always include FINAL synthesis last, depending on all prior tasks.
4) Always include knowledge_graph after research/analysis (even if output is light).
5) Use prior context: respect last_run_time and known_urls to avoid duplicates.
6) Map preferences into task parameters (web_search, analysis, knowledge_graph, conflict_detection, etc.).
7) Ensure every task has realistic timeout and estimates.

SYNTHESIS TASK REQUIREMENTS:
- Output MUST be itemized with items[] each containing title, summary, category (top|policy|politics|legal|markets|other), tags[], published_at (ISO8601), sources[] (authoritative link, archived_url when feasible), importance (0..1), confidence (0..1).
- 100%% of items must include published_at and at least one source; drop items otherwise.
- Provide sections for Top, Policy, Debate/Politics, Legal/Regulatory, and Quick Hits/Other.
- Sort items by importance and recency derived from analysis.
- Keep summary concise (2–3 sentences) and move depth to detailed_report with headings.

Respond with ONLY the JSON object.`, thought.Content, ctxBlock, policyBlock, prefBlock, caps)
}

// parsePlanningResponse parses the LLM response into a PlanningResult
func (p *Planner) parsePlanningResponse(response string) (PlanningResult, error) {
	// Extract JSON from response using balanced brace scanning
	jsonStr := ""
	start := -1
	depth := 0
	for i, ch := range response {
		if ch == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if ch == '}' {
			if depth > 0 {
				depth--
			}
			if depth == 0 && start != -1 {
				jsonStr = response[start : i+1]
				break
			}
		}
	}
	if jsonStr == "" {
		return PlanningResult{}, fmt.Errorf("no JSON found in response")
	}

	jsonBytes := []byte(jsonStr)
	if err := plannerv1.ValidatePlanDocument(jsonBytes); err != nil {
		return PlanningResult{}, fmt.Errorf("plan schema validation failed: %w", err)
	}

	var graph plannerv1.PlanDocument
	if err := json.Unmarshal(jsonBytes, &graph); err != nil {
		return PlanningResult{}, fmt.Errorf("failed to decode plan graph: %w", err)
	}
	if graph.Version == "" {
		graph.Version = "v1"
	}

	tasks := make([]AgentTask, 0, len(graph.Tasks))
	for _, node := range graph.Tasks {
		id := node.ID
		if id == "" {
			id = uuid.New().String()
		}

		timeout := p.config.Agents.AgentTimeout
		if node.Timeout != "" {
			if d, err := time.ParseDuration(node.Timeout); err == nil {
				timeout = d
			}
		}

		task := AgentTask{
			ID:          id,
			Type:        node.Type,
			Description: node.Description,
			Priority:    node.Priority,
			Parameters:  node.Parameters,
			DependsOn:   node.DependsOn,
			Timeout:     timeout,
			CreatedAt:   time.Now(),
		}
		tasks = append(tasks, task)
	}

	estimatedCost := 0.0
	var totalTime time.Duration = 5 * time.Minute
	if graph.Estimates != nil {
		estimatedCost = graph.Estimates.TotalCost
		if graph.Estimates.TotalTime != "" {
			if d, err := time.ParseDuration(graph.Estimates.TotalTime); err == nil {
				totalTime = d
			}
		}
	}

	confidence := graph.Confidence
	if confidence == 0 {
		confidence = 0.8
	}

	return PlanningResult{
		Tasks:          tasks,
		ExecutionOrder: graph.ExecutionOrder,
		EstimatedCost:  estimatedCost,
		EstimatedTime:  totalTime,
		Confidence:     confidence,
		Budget:         graph.Budget,
		Edges:          graph.Edges,
		Estimates:      graph.Estimates,
		Graph:          &graph,
		RawJSON:        jsonBytes,
	}, nil
}

// ValidatePlan validates if a plan is feasible
func (p *Planner) ValidatePlan(plan PlanningResult) error {
	if len(plan.Tasks) == 0 {
		return fmt.Errorf("plan has no tasks")
	}

	// Check for circular dependencies
	if err := p.checkCircularDependencies(plan.Tasks); err != nil {
		return fmt.Errorf("circular dependencies detected: %w", err)
	}

	// Check for missing dependencies
	if err := p.checkMissingDependencies(plan.Tasks); err != nil {
		return fmt.Errorf("missing dependencies: %w", err)
	}

	// Validate task types
	for _, task := range plan.Tasks {
		if !p.isValidTaskType(task.Type) {
			return fmt.Errorf("invalid task type: %s", task.Type)
		}
	}

	// Ensure mandatory phases are present
	hasSynth := false
	hasKG := false
	for _, t := range plan.Tasks {
		if t.Type == "synthesis" {
			hasSynth = true
		}
		if t.Type == "knowledge_graph" {
			hasKG = true
		}
	}
	if !hasSynth {
		return fmt.Errorf("plan must include a final synthesis task")
	}
	if !hasKG {
		return fmt.Errorf("plan must include a knowledge_graph task")
	}

	// No cost/time gating — focus on quality

	return nil
}

// OptimizePlan optimizes a plan for cost/time/quality
func (p *Planner) OptimizePlan(plan PlanningResult, constraints map[string]interface{}) (PlanningResult, error) {
	// Get constraints
	maxCost, _ := constraints["max_cost"].(float64)
	maxTime, _ := constraints["max_time"].(time.Duration)
	_ = constraints["quality"] // quality constraint not used in current implementation

	if maxCost == 0 {
		maxCost = 10.0
	}
	if maxTime == 0 {
		maxTime = 5 * time.Minute
	}

	// If plan is already within constraints, return as is
	if plan.EstimatedCost <= maxCost && plan.EstimatedTime <= maxTime {
		return plan, nil
	}

	// Create optimization prompt
	prompt := p.createOptimizationPrompt(plan, constraints)

	// Get optimization model (use same as planning for consistency)
	model := p.config.LLM.Routing.Planning

	// Generate optimized plan
	ctx2, cancel2 := context.WithTimeout(context.Background(), p.config.General.DefaultTimeout)
	defer cancel2()

	response, err := p.llmProvider.Generate(ctx2, prompt, model, nil)
	if err != nil {
		return plan, fmt.Errorf("failed to optimize plan: %w", err)
	}

	// Parse optimized plan
	optimizedPlan, err := p.parsePlanningResponse(response)
	if err != nil {
		return plan, fmt.Errorf("failed to parse optimized plan: %w", err)
	}

	// Validate optimized plan
	if err := p.ValidatePlan(optimizedPlan); err != nil {
		return plan, fmt.Errorf("optimized plan validation failed: %w", err)
	}

	if plan.TemporalPolicy != nil {
		attachTemporalPolicy(&optimizedPlan, plan.TemporalPolicy)
	}

	return optimizedPlan, nil
}

// createOptimizationPrompt creates a prompt for plan optimization
func (p *Planner) createOptimizationPrompt(plan PlanningResult, constraints map[string]interface{}) string {
	maxCost, _ := constraints["max_cost"].(float64)
	maxTime, _ := constraints["max_time"].(time.Duration)
	quality, _ := constraints["quality"].(string)

	return fmt.Sprintf(`You are optimizing an execution plan to meet specific constraints.

CURRENT PLAN:
- Tasks: %d
- Estimated Cost: $%.2f
- Estimated Time: %v
- Confidence: %.2f

CONSTRAINTS:
- Max Cost: $%.2f
- Max Time: %v
- Quality: %s

OPTIMIZATION STRATEGIES:
1. Combine similar tasks
2. Reduce search scope for non-critical information
3. Use faster/cheaper models for less critical tasks
4. Remove redundant tasks
5. Optimize task dependencies

TASK PRIORITIES:
- Priority 1: Essential for user's core request
- Priority 2: Important for comprehensive coverage
- Priority 3: Nice to have, can be reduced or removed
- Priority 4-5: Optional, can be removed if needed

Optimize the plan to meet the constraints while maintaining the highest possible quality for the user's request.

OUTPUT FORMAT: Same JSON format as planning, but with optimized tasks.`,
		len(plan.Tasks), plan.EstimatedCost, plan.EstimatedTime, plan.Confidence,
		maxCost, maxTime, quality)
}

// checkCircularDependencies checks for circular dependencies in tasks
func (p *Planner) checkCircularDependencies(tasks []AgentTask) error {
	// Create dependency graph
	deps := make(map[string][]string)
	for _, task := range tasks {
		deps[task.ID] = task.DependsOn
	}

	// Check for cycles using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(string) bool
	hasCycle = func(taskID string) bool {
		if recStack[taskID] {
			return true
		}
		if visited[taskID] {
			return false
		}

		visited[taskID] = true
		recStack[taskID] = true

		for _, dep := range deps[taskID] {
			if hasCycle(dep) {
				return true
			}
		}

		recStack[taskID] = false
		return false
	}

	for _, task := range tasks {
		if !visited[task.ID] {
			if hasCycle(task.ID) {
				return fmt.Errorf("circular dependency detected")
			}
		}
	}

	return nil
}

// checkMissingDependencies checks for missing task dependencies
func (p *Planner) checkMissingDependencies(tasks []AgentTask) error {
	taskIDs := make(map[string]bool)
	for _, task := range tasks {
		taskIDs[task.ID] = true
	}

	for _, task := range tasks {
		for _, depID := range task.DependsOn {
			if !taskIDs[depID] {
				return fmt.Errorf("task %s depends on missing task %s", task.ID, depID)
			}
		}
	}

	return nil
}

// isValidTaskType checks if a task type is valid
func (p *Planner) isValidTaskType(taskType string) bool {
	if p.registry != nil {
		if _, ok := p.registry.Tool(taskType); ok {
			return true
		}
		return false
	}

	validTypes := []string{
		"research",
		"analysis",
		"synthesis",
		"conflict_detection",
		"highlight_management",
		"knowledge_graph",
	}

	for _, validType := range validTypes {
		if taskType == validType {
			return true
		}
	}

	return false

}
