package core

import (
    "context"
    "testing"
    "time"

    agentcfg "github.com/mohammad-safakhou/newser/internal/agent/config"
)

type dummyLLM struct{}
func (d *dummyLLM) Generate(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, error) { return "", nil }
func (d *dummyLLM) GenerateWithTokens(ctx context.Context, prompt string, model string, options map[string]interface{}) (string, int64, int64, error) { return "", 0, 0, nil }
func (d *dummyLLM) GetAvailableModels() []string { return nil }
func (d *dummyLLM) GetModelInfo(model string) (ModelInfo, error) { return ModelInfo{}, nil }
func (d *dummyLLM) CalculateCost(inputTokens, outputTokens int64, model string) float64 { return 0 }

func TestPlanner_ParsePlanningResponse_CoerceNumericIDs(t *testing.T) {
    cfg := &agentcfg.Config{}
    cfg.Agents.AgentTimeout = 2 * time.Minute
    p := NewPlanner(cfg, &dummyLLM{}, nil)

    response := `Some preface
    {"tasks":[{"id":1,"type":"research","description":"do","priority":1,"parameters":{},"depends_on":[],"timeout":"30s","estimated_cost":0.1,"estimated_time":"10s"}],
      "execution_order":[],"estimated_total_cost":0.1,"estimated_total_time":"1m","confidence":0.8}`

    plan, err := p.parsePlanningResponse(response)
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if len(plan.Tasks) != 1 { t.Fatalf("expected 1 task, got %d", len(plan.Tasks)) }
    if plan.Tasks[0].ID == "" { t.Fatalf("expected coerced string ID, got empty") }
}


