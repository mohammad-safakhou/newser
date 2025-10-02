package planner

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "embed"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed plan_schema.json
var planSchemaJSON string

// PlanDocument represents the canonical JSON plan graph produced by the planner LLM.
type PlanDocument struct {
	Version        string                 `json:"version"`
	PlanID         string                 `json:"plan_id,omitempty"`
	CreatedAt      string                 `json:"created_at,omitempty"`
	UpdatedAt      string                 `json:"updated_at,omitempty"`
	Description    string                 `json:"description,omitempty"`
	Reasoning      string                 `json:"reasoning,omitempty"`
	Confidence     float64                `json:"confidence,omitempty"`
	ExecutionOrder []string               `json:"execution_order,omitempty"`
	Tasks          []PlanTask             `json:"tasks"`
	Edges          []PlanEdge             `json:"edges,omitempty"`
	Budget         *PlanBudget            `json:"budget,omitempty"`
	Estimates      *PlanEstimates         `json:"estimates,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
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

// ValidatePlanDocument validates the provided JSON bytes against the plan schema.
func ValidatePlanDocument(data []byte) error {
	schema, err := PlanSchema()
	if err != nil {
		return err
	}
	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("plan is not valid JSON: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("plan does not match schema: %w", err)
	}
	return nil
}
