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
		"temperature": 0.3, // Lower temperature for more consistent planning
		"max_tokens":  2000,
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

	// Optimize the plan
	optimizedPlan, err := p.OptimizePlan(plan, map[string]interface{}{
		"max_cost": 10.0, // $10 max cost
		"max_time": 5 * time.Minute,
		"quality":  "high",
	})
	if err != nil {
		p.logger.Printf("Warning: plan optimization failed: %v, using original plan", err)
		optimizedPlan = plan
	}

	processingTime := time.Since(startTime)
	p.logger.Printf("Planning completed in %v with %d tasks", processingTime, len(optimizedPlan.Tasks))

	return optimizedPlan, nil
}

// createPlanningPrompt creates a comprehensive prompt for planning
func (p *Planner) createPlanningPrompt(thought UserThought) string {
    // Render prior context for incremental planning
    ctxBlock := ""
    if thought.Context != nil {
        if v, ok := thought.Context["last_run_time"].(string); ok && v != "" {
            ctxBlock += fmt.Sprintf("Last run time: %s\n", v)
        }
        if v, ok := thought.Context["prev_summary"].(string); ok && v != "" {
            if len(v) > 800 { v = v[:800] + "..." }
            ctxBlock += fmt.Sprintf("Previous summary: %s\n", v)
        }
        if arr, ok := thought.Context["known_urls"].([]string); ok && len(arr) > 0 {
            ctxBlock += fmt.Sprintf("Known URLs: %d (avoid duplicates)\n", len(arr))
        }
        if kg, ok := thought.Context["knowledge_graph"].(map[string]interface{}); ok {
            if nodes, ok := kg["nodes"].([]interface{}); ok { ctxBlock += fmt.Sprintf("KG nodes: %d\n", len(nodes)) }
            if edges, ok := kg["edges"].([]interface{}); ok { ctxBlock += fmt.Sprintf("KG edges: %d\n", len(edges)) }
        }
    }
    if ctxBlock != "" { ctxBlock = "\nPRIOR CONTEXT:\n" + ctxBlock }

    return fmt.Sprintf(`You are an intelligent planning agent that creates execution plans for news research and analysis.%s

USER THOUGHT: %s

AVAILABLE AGENTS:
- Research Agent: Searches for information from multiple sources (news APIs, web search, social media)
- Analysis Agent: Analyzes content for relevance, credibility, and importance
- Synthesis Agent: Combines and summarizes information into coherent reports
- Conflict Detection Agent: Identifies and resolves conflicting information
- Highlight Agent: Identifies and manages key highlights and pinned information
 - Knowledge Graph Agent: Extracts entities/relations into a knowledge graph

AVAILABLE SOURCES:
- News APIs (NewsAPI, etc.)
- Web Search (Brave, Serper)
- Social Media monitoring
- Academic papers (if relevant)
- Previous knowledge graph data

TASK TYPES:
- research: Gather information from sources
- analysis: Analyze content and relevance
- synthesis: Combine information into reports
- conflict_detection: Identify and resolve conflicts
- highlight_management: Manage highlights and pinned content
- knowledge_graph: Update knowledge graph

PLANNING REQUIREMENTS:
1. Break down the user's thought into specific, actionable tasks
2. Consider dependencies between tasks (e.g., research must happen before analysis)
3. Prioritize tasks based on importance and dependencies
4. Estimate costs and time for each task
5. Consider user preferences and context
6. Plan for conflict detection and resolution
7. Include highlight management for ongoing topics
8. You MUST include at least one final "synthesis" task at the end that depends on all prior tasks so the results are coherent.
9. You MUST include a "knowledge_graph" task (it can be lightweight if little is known). It should depend on research/analysis results so it can extract entities/relations.
10. If helpful, you MAY include intermediate synthesis steps, but still include a final synthesis as the last step.
11. Keep agents intelligent about effort: for low-information cases, set narrower parameters or scope; for rich topics, broaden depth.

OUTPUT FORMAT (JSON):
{
  "tasks": [
    {
      "id": "unique_task_id",
      "type": "task_type",
      "description": "Detailed description of what this task should do",
      "priority": 1-5,
      "parameters": {
        "query": "search query",
        "sources": ["source1", "source2"],
        "filters": {},
        "options": {}
      },
      "depends_on": ["task_id1", "task_id2"],
      "timeout": "2m",
      "estimated_cost": 0.5,
      "estimated_time": "30s"
    }
  ],
  "execution_order": ["task1", "task2", "task3"],
  "estimated_total_cost": 2.5,
  "estimated_total_time": "3m",
  "confidence": 0.85,
  "reasoning": "Explanation of why this plan was chosen"
}

Create a comprehensive plan that will effectively address the user's thought. Focus on accuracy and relevance over speed. Ensure the final synthesis is last and knowledge_graph is present.`, ctxBlock, thought.Content)

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
        if t.Type == "synthesis" { hasSynth = true }
        if t.Type == "knowledge_graph" { hasKG = true }
    }
    if !hasSynth {
        return fmt.Errorf("plan must include a final synthesis task")
    }
    if !hasKG {
        return fmt.Errorf("plan must include a knowledge_graph task")
    }

	// Check cost and time estimates
	if plan.EstimatedCost > 20.0 {
		return fmt.Errorf("estimated cost too high: $%.2f", plan.EstimatedCost)
	}

	if plan.EstimatedTime > 10*time.Minute {
		return fmt.Errorf("estimated time too long: %v", plan.EstimatedTime)
	}

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
