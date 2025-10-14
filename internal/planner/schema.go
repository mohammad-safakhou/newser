package planner

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed plan_schema.json
var planSchemaJSON string

// PlanDocument represents the canonical JSON plan graph produced by the planner LLM.
type PlanDocument struct {
	Version         string                 `json:"version"`
	PlanID          string                 `json:"plan_id,omitempty"`
	CreatedAt       string                 `json:"created_at,omitempty"`
	UpdatedAt       string                 `json:"updated_at,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Reasoning       string                 `json:"reasoning,omitempty"`
	Confidence      float64                `json:"confidence,omitempty"`
	ExecutionOrder  []string               `json:"execution_order,omitempty"`
	ExecutionLayers []PlanStage            `json:"execution_layers,omitempty"`
	Tasks           []PlanTask             `json:"tasks"`
	Edges           []PlanEdge             `json:"edges,omitempty"`
	Budget          *PlanBudget            `json:"budget,omitempty"`
	Estimates       *PlanEstimates         `json:"estimates,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// PlanStage describes an execution checkpoint layer inferred from the DAG.
type PlanStage struct {
	Stage           string   `json:"stage"`
	Tasks           []string `json:"tasks"`
	CheckpointToken string   `json:"checkpoint_token,omitempty"`
}

// PlanTask models a single node in the plan DAG.
type PlanTask struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	Name            string                 `json:"name,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Priority        int                    `json:"priority,omitempty"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
	DependsOn       []string               `json:"depends_on,omitempty"`
	Timeout         string                 `json:"timeout,omitempty"`
	EstimatedCost   float64                `json:"estimated_cost,omitempty"`
	EstimatedTime   string                 `json:"estimated_time,omitempty"`
	EstimatedTokens int                    `json:"estimated_tokens,omitempty"`
	Outputs         []string               `json:"outputs,omitempty"`
}

// PlanEdge connects two tasks in the graph.
type PlanEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind,omitempty"`
	Condition string `json:"condition,omitempty"`
}

// PlanBudget constrains the execution.
type PlanBudget struct {
	MaxCost   float64 `json:"max_cost,omitempty"`
	MaxTime   string  `json:"max_time,omitempty"`
	MaxTokens int64   `json:"max_tokens,omitempty"`
}

// PlanEstimates summarises aggregate forecasts for the plan.
type PlanEstimates struct {
	TotalCost   float64 `json:"total_cost,omitempty"`
	TotalTime   string  `json:"total_time,omitempty"`
	TotalTokens int64   `json:"total_tokens,omitempty"`
}

var (
	compileOnce sync.Once
	planSchema  *jsonschema.Schema
	compileErr  error
)

// PlanSchema returns the compiled JSON Schema for planner graph documents.
func PlanSchema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("plan_schema.json", strings.NewReader(planSchemaJSON)); err != nil {
			compileErr = fmt.Errorf("add schema resource: %w", err)
			return
		}
		schema, err := compiler.Compile("plan_schema.json")
		if err != nil {
			compileErr = fmt.Errorf("compile planner schema: %w", err)
			return
		}
		planSchema = schema
	})
	return planSchema, compileErr
}

// ValidationError pinpoints a single schema or structural issue within a plan document.
type ValidationError struct {
	Path    string
	Message string
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Path)
}

// ValidationErrors represents multiple actionable validation failures.
type ValidationErrors []ValidationError

// Error implements the error interface.
func (ves ValidationErrors) Error() string {
	switch len(ves) {
	case 0:
		return ""
	case 1:
		return ves[0].Error()
	default:
		var b strings.Builder
		b.WriteString("plan validation errors:")
		for _, v := range ves {
			b.WriteString("\n - ")
			if v.Path != "" {
				b.WriteString(v.Path)
				b.WriteString(": ")
			}
			b.WriteString(v.Message)
		}
		return b.String()
	}
}

// ValidatePlanDocument validates the provided JSON bytes against the plan schema and structural rules.
func ValidatePlanDocument(data []byte) error {
	_, _, err := NormalizePlanDocument(data)
	return err
}

// NormalizePlanDocument validates and enriches plan JSON, returning a canonical document and normalised JSON bytes.
func NormalizePlanDocument(data []byte) (*PlanDocument, []byte, error) {
	schema, err := PlanSchema()
	if err != nil {
		return nil, nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("plan is not valid JSON: %w", err)
	}
	if err := schema.Validate(raw); err != nil {
		return nil, nil, convertSchemaError(err)
	}

	var doc PlanDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("plan decode failed: %w", err)
	}
	if doc.Version == "" {
		doc.Version = "v1"
	}

	validations := validatePlanStructure(&doc)
	if len(validations) > 0 {
		return nil, nil, validations
	}

	order, layers, topoErrs := computeExecutionHints(doc.Tasks)
	if len(topoErrs) > 0 {
		return nil, nil, topoErrs
	}
	doc.ExecutionOrder = order
	doc.ExecutionLayers = make([]PlanStage, len(layers))
	for i, group := range layers {
		stageName := fmt.Sprintf("stage-%02d", i+1)
		doc.ExecutionLayers[i] = PlanStage{
			Stage:           stageName,
			Tasks:           group,
			CheckpointToken: stageName,
		}
	}

	normalized, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("normalise plan: %w", err)
	}
	return &doc, normalized, nil
}

func convertSchemaError(err error) error {
	var vErr *jsonschema.ValidationError
	if errors.As(err, &vErr) {
		flattened := flattenValidationError(vErr)
		if len(flattened) == 1 {
			return flattened[0]
		}
		return flattened
	}
	return err
}

func flattenValidationError(err *jsonschema.ValidationError) ValidationErrors {
	if err == nil {
		return nil
	}
	if len(err.Causes) == 0 {
		return ValidationErrors{schemaValidationError(err)}
	}
	var out ValidationErrors
	for _, cause := range err.Causes {
		out = append(out, flattenValidationError(cause)...)
	}
	if len(out) == 0 {
		out = append(out, schemaValidationError(err))
	}
	return out
}

func schemaValidationError(err *jsonschema.ValidationError) ValidationError {
	return ValidationError{
		Path:    humaniseJSONPointer(err.InstanceLocation),
		Message: err.Message,
	}
}

func humaniseJSONPointer(ptr string) string {
	if ptr == "" || ptr == "#" {
		return ""
	}
	ptr = strings.TrimPrefix(ptr, "#")
	ptr = strings.TrimPrefix(ptr, "/")
	if ptr == "" {
		return ""
	}
	parts := strings.Split(ptr, "/")
	formatted := make([]string, 0, len(parts))
	for idx, part := range parts {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		if _, err := strconv.Atoi(part); err == nil {
			formatted = append(formatted, fmt.Sprintf("[%s]", part))
			continue
		}
		if idx == 0 {
			formatted = append(formatted, part)
		} else {
			formatted = append(formatted, "."+part)
		}
	}
	return strings.Join(formatted, "")
}

func validatePlanStructure(doc *PlanDocument) ValidationErrors {
	var errs ValidationErrors
	if len(doc.Tasks) == 0 {
		errs = append(errs, ValidationError{Path: "tasks", Message: "at least one task is required"})
		return errs
	}

	index := make(map[string]int, len(doc.Tasks))
	for i, task := range doc.Tasks {
		if prev, exists := index[task.ID]; exists {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("tasks[%d].id", i),
				Message: fmt.Sprintf("duplicate task id %q (first seen at tasks[%d])", task.ID, prev),
			})
			continue
		}
		index[task.ID] = i
	}

	for i, task := range doc.Tasks {
		seenDeps := make(map[string]struct{}, len(task.DependsOn))
		for j, dep := range task.DependsOn {
			path := fmt.Sprintf("tasks[%d].depends_on[%d]", i, j)
			if dep == task.ID {
				errs = append(errs, ValidationError{Path: path, Message: "task cannot depend on itself"})
				continue
			}
			if _, ok := index[dep]; !ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("unknown dependency %q", dep)})
				continue
			}
			if _, ok := seenDeps[dep]; ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate dependency %q", dep)})
				continue
			}
			seenDeps[dep] = struct{}{}
		}
		if task.Timeout != "" {
			if _, err := time.ParseDuration(task.Timeout); err != nil {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("tasks[%d].timeout", i),
					Message: fmt.Sprintf("invalid timeout %q: %v", task.Timeout, err),
				})
			}
		}
		if task.EstimatedTime != "" {
			if _, err := time.ParseDuration(task.EstimatedTime); err != nil {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("tasks[%d].estimated_time", i),
					Message: fmt.Sprintf("invalid estimated_time %q: %v", task.EstimatedTime, err),
				})
			}
		}
	}

	for i, edge := range doc.Edges {
		if edge.From != "" {
			if _, ok := index[edge.From]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("edges[%d].from", i),
					Message: fmt.Sprintf("unknown task id %q", edge.From),
				})
			}
		}
		if edge.To != "" {
			if _, ok := index[edge.To]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("edges[%d].to", i),
					Message: fmt.Sprintf("unknown task id %q", edge.To),
				})
			}
		}
		if edge.From != "" && edge.From == edge.To {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("edges[%d]", i),
				Message: "edge must connect distinct tasks",
			})
		}
	}

	if doc.Budget != nil && doc.Budget.MaxTime != "" {
		if _, err := time.ParseDuration(doc.Budget.MaxTime); err != nil {
			errs = append(errs, ValidationError{
				Path:    "budget.max_time",
				Message: fmt.Sprintf("invalid duration %q: %v", doc.Budget.MaxTime, err),
			})
		}
	}
	if doc.Estimates != nil && doc.Estimates.TotalTime != "" {
		if _, err := time.ParseDuration(doc.Estimates.TotalTime); err != nil {
			errs = append(errs, ValidationError{
				Path:    "estimates.total_time",
				Message: fmt.Sprintf("invalid duration %q: %v", doc.Estimates.TotalTime, err),
			})
		}
	}

	if len(doc.ExecutionOrder) > 0 {
		errs = append(errs, validateExecutionOrder(doc.ExecutionOrder, doc.Tasks, index)...)
	}

	return errs
}

func validateExecutionOrder(order []string, tasks []PlanTask, index map[string]int) ValidationErrors {
	var errs ValidationErrors
	scheduled := make(map[string]int, len(order))
	for pos, id := range order {
		path := fmt.Sprintf("execution_order[%d]", pos)
		idx, ok := index[id]
		if !ok {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("unknown task id %q", id)})
			continue
		}
		if prev, dup := scheduled[id]; dup {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("task %q already scheduled at position %d", id, prev)})
			continue
		}
		for _, dep := range tasks[idx].DependsOn {
			if _, ok := scheduled[dep]; !ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("dependency %q must appear before task %q", dep, id)})
				break
			}
		}
		scheduled[id] = pos
	}

	if len(scheduled) != len(tasks) {
		missing := make([]string, 0, len(tasks)-len(scheduled))
		for id := range index {
			if _, ok := scheduled[id]; !ok {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		errs = append(errs, ValidationError{
			Path:    "execution_order",
			Message: fmt.Sprintf("missing tasks: %s", strings.Join(missing, ", ")),
		})
	}

	if len(order) > len(tasks) {
		extras := make([]string, 0)
		for _, id := range order {
			if _, ok := index[id]; !ok {
				extras = append(extras, id)
			}
		}
		if len(extras) > 0 {
			errs = append(errs, ValidationError{
				Path:    "execution_order",
				Message: fmt.Sprintf("contains unknown tasks: %s", strings.Join(extras, ", ")),
			})
		}
	}

	return errs
}

func computeExecutionHints(tasks []PlanTask) ([]string, [][]string, ValidationErrors) {
	index := make(map[string]int, len(tasks))
	for i, task := range tasks {
		index[task.ID] = i
	}

	adjacency := make(map[string][]string, len(tasks))
	indegree := make(map[string]int, len(tasks))
	for id := range index {
		indegree[id] = 0
	}
	for _, task := range tasks {
		for _, dep := range task.DependsOn {
			indegree[task.ID]++
			adjacency[dep] = append(adjacency[dep], task.ID)
		}
	}

	queue := make([]string, 0)
	for id, deg := range indegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		return index[queue[i]] < index[queue[j]]
	})

	order := make([]string, 0, len(tasks))
	layers := make([][]string, 0)

	for len(queue) > 0 {
		current := append([]string(nil), queue...)
		layers = append(layers, current)
		queue = queue[:0]
		for _, id := range current {
			order = append(order, id)
			for _, next := range adjacency[id] {
				indegree[next]--
				if indegree[next] == 0 {
					queue = append(queue, next)
				}
			}
		}
		if len(queue) > 0 {
			sort.Slice(queue, func(i, j int) bool {
				return index[queue[i]] < index[queue[j]]
			})
		}
	}

	if len(order) != len(tasks) {
		stuck := make([]string, 0)
		for id, deg := range indegree {
			if deg > 0 {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, nil, ValidationErrors{{
			Message: fmt.Sprintf("dependency cycle detected; unresolved tasks: %s", strings.Join(stuck, ", ")),
		}}
	}

	return order, layers, nil
}
