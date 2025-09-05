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
)

// Planner creates intelligent execution plans for user thoughts
type Planner struct {
	config      *config.Config
	llmProvider LLMProvider
	telemetry   *telemetry.Telemetry
	logger      *log.Logger
}

// NewPlanner creates a new planner instance
func NewPlanner(cfg *config.Config, llmProvider LLMProvider, telemetry *telemetry.Telemetry) *Planner {
	return &Planner{
		config:      cfg,
		llmProvider: llmProvider,
		telemetry:   telemetry,
		logger:      log.New(log.Writer(), "[PLANNER] ", log.LstdFlags),
	}
}

// Plan creates an execution plan for a user thought
func (p *Planner) Plan(ctx context.Context, thought UserThought) (PlanningResult, error) {
	startTime := time.Now()

	// Create planning prompt
	prompt := p.createPlanningPrompt(thought)

	// Get the planning model
	model := p.config.LLM.Routing.Planning

	// Generate plan using LLM
	response, err := p.llmProvider.Generate(ctx, prompt, model, map[string]interface{}{
		"temperature": 0.2,
		"max_tokens":  1800,
	})
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

	processingTime := time.Since(startTime)
	p.logger.Printf("Planning completed in %v with %d tasks", processingTime, len(plan.Tasks))

	return plan, nil
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

	return fmt.Sprintf(`ROLE: Senior news intelligence planner focused on accuracy and usefulness. Ignore money/time costs; minimize tasks while maximizing quality.

USER THOUGHT / TOPIC:
%s%s%s

AVAILABLE AGENTS:
- research: gather information from sources
- analysis: evaluate relevance, credibility, importance
- synthesis: produce coherent final report (MUST be last)
- conflict_detection: find and resolve conflicting claims
- highlight_management: extract key highlights
- knowledge_graph: extract entities/relations (always include; can be light)

PLANNING PRINCIPLES:
1) Create the fewest, highest‑impact tasks.
2) Set dependencies (research -> analysis -> downstream tasks).
3) Always include FINAL synthesis last, depending on all prior tasks.
4) Always include knowledge_graph after research/analysis (even if output is small).
5) Use prior context: since last_run_time; avoid known_urls duplicates.
6) Encode necessary parameters (query, filters, since, exclude_urls) in tasks.
7) No cost/time estimation; avoid redundant steps.

OUTPUT JSON:
{
  "tasks": [
    {
      "id": "unique_task_id",
      "type": "research|analysis|synthesis|conflict_detection|highlight_management|knowledge_graph",
      "description": "what to do",
      "priority": 1-5,
      "parameters": {
        "query": "...",
        "filters": {},
        "since": "RFC3339 if incremental",
        "exclude_urls": ["..."]
      },
      "depends_on": ["task_id1"],
      "timeout": "2m"
    }
  ],
  "execution_order": ["task1", "task2"],
  "confidence": 0.85,
  "reasoning": "why this plan"
}

Return ONLY JSON. Ensure final synthesis is last and knowledge_graph is present.`, thought.Content, prefBlock, ctxBlock)
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

	var rawPlan struct {
		Tasks []struct {
			ID            string                 `json:"id"`
			Type          string                 `json:"type"`
			Description   string                 `json:"description"`
			Priority      int                    `json:"priority"`
			Parameters    map[string]interface{} `json:"parameters"`
			DependsOn     []string               `json:"depends_on"`
			Timeout       string                 `json:"timeout"`
			EstimatedCost float64                `json:"estimated_cost"`
			EstimatedTime string                 `json:"estimated_time"`
		} `json:"tasks"`
		ExecutionOrder     []string `json:"execution_order"`
		EstimatedTotalCost float64  `json:"estimated_total_cost"`
		EstimatedTotalTime string   `json:"estimated_total_time"`
		Confidence         float64  `json:"confidence"`
		Reasoning          string   `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &rawPlan); err != nil {
		// lenient fallback: coerce numeric IDs
		var generic map[string]interface{}
		if err2 := json.Unmarshal([]byte(jsonStr), &generic); err2 == nil {
			var tasks []AgentTask
			if tAny, ok := generic["tasks"].([]interface{}); ok {
				for _, ti := range tAny {
					tmap, _ := ti.(map[string]interface{})
					id := ""
					if v, ok := tmap["id"].(string); ok {
						id = v
					} else if fv, ok := tmap["id"].(float64); ok {
						id = fmt.Sprintf("%.0f", fv)
					}
					ttype, _ := tmap["type"].(string)
					desc, _ := tmap["description"].(string)
					prio := 0
					if pv, ok := tmap["priority"].(float64); ok {
						prio = int(pv)
					}
					params := map[string]interface{}{}
					if pm, ok := tmap["parameters"].(map[string]interface{}); ok {
						params = pm
					}
					var deps []string
					if dl, ok := tmap["depends_on"].([]interface{}); ok {
						for _, di := range dl {
							if ds, ok := di.(string); ok {
								deps = append(deps, ds)
							}
						}
					}
					timeout := p.config.Agents.AgentTimeout
					if ts, ok := tmap["timeout"].(string); ok {
						if d, e := time.ParseDuration(ts); e == nil {
							timeout = d
						}
					}
					if id == "" {
						id = uuid.New().String()
					}
					tasks = append(tasks, AgentTask{ID: id, Type: ttype, Description: desc, Priority: prio, Parameters: params, DependsOn: deps, Timeout: timeout, CreatedAt: time.Now()})
				}
			}
			totalTime := 5 * time.Minute
			if et, ok := generic["estimated_total_time"].(string); ok {
				if d, e := time.ParseDuration(et); e == nil {
					totalTime = d
				}
			}
			return PlanningResult{Tasks: tasks, ExecutionOrder: []string{}, EstimatedCost: 0, EstimatedTime: totalTime, Confidence: 0.8}, nil
		}
		return PlanningResult{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Convert to PlanningResult
	var tasks []AgentTask
	for _, rawTask := range rawPlan.Tasks {
		// Generate ID if not provided
		if rawTask.ID == "" {
			rawTask.ID = uuid.New().String()
		}

		// Parse timeout
		timeout, err := time.ParseDuration(rawTask.Timeout)
		if err != nil {
			timeout = p.config.Agents.AgentTimeout
		}

		// Parse estimated time (not used in current implementation)
		_, err = time.ParseDuration(rawTask.EstimatedTime)
		if err != nil {
			// Use default time
		}

		task := AgentTask{
			ID:          rawTask.ID,
			Type:        rawTask.Type,
			Description: rawTask.Description,
			Priority:    rawTask.Priority,
			Parameters:  rawTask.Parameters,
			DependsOn:   rawTask.DependsOn,
			Timeout:     timeout,
			CreatedAt:   time.Now(),
		}

		tasks = append(tasks, task)
	}

	// Parse total estimated time
	totalTime, err := time.ParseDuration(rawPlan.EstimatedTotalTime)
	if err != nil {
		totalTime = 5 * time.Minute
	}

	return PlanningResult{
		Tasks:          tasks,
		ExecutionOrder: rawPlan.ExecutionOrder,
		EstimatedCost:  rawPlan.EstimatedTotalCost,
		EstimatedTime:  totalTime,
		Confidence:     rawPlan.Confidence,
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

	response, err := p.llmProvider.Generate(ctx2, prompt, model, map[string]interface{}{
		"temperature": 0.2,
		"max_tokens":  1500,
	})
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
