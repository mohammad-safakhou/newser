package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type OpenAIPlanner struct {
	APIKey, Model string
	Temp          float64
}

func (p OpenAIPlanner) Plan(ctx context.Context, in PlannerInput) (PlannerOutput, error) {
	payload := map[string]any{
		"model":           p.Model,
		"temperature":     p.Temp,
		"response_format": map[string]any{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": `You are a planning controller. Output STRICT JSON PlannerOutput.
- You can create a DAG of actions. Kinds: "tool", "spawn_master".
- Use depends_on to order steps.
- Prefer minimal, effective steps to satisfy the Topic.
- Always include short reasons.
- Stop=true when the topic objective is satisfied or no more useful actions exist.`},
			{"role": "user", "content": "INPUT:\n" + mustJSON(in)},
			{"role": "user", "content": `Return JSON: {"stop":bool,"why_stop":string,"actions":[{"id":string,"kind":"tool"|"spawn_master","depends_on":[string],"capability":string,"tool":string,"args":object,"sub_goal":object,"expect":object,"on_fail":object,"reason":string}]}`},
		},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+envOr("OPENAI_API_KEY", ""))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return PlannerOutput{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		x, _ := io.ReadAll(resp.Body)
		return PlannerOutput{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(x))
	}
	var raw struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return PlannerOutput{}, err
	}
	if len(raw.Choices) == 0 {
		return PlannerOutput{}, errors.New("no choices")
	}
	var out PlannerOutput
	if err := json.Unmarshal([]byte(raw.Choices[0].Message.Content), &out); err != nil {
		return PlannerOutput{}, fmt.Errorf("bad JSON from model: %w; content=%s", err, raw.Choices[0].Message.Content)
	}
	return out, nil
}

// ==========================================================
// Planner contracts (LLM)
// ==========================================================

type PlannerInput struct {
	RunID        string               `json:"run_id"`
	NodeName     string               `json:"node_name"`
	Depth        int                  `json:"depth"`
	Topic        Topic                `json:"topic"`
	Tools        []MCPToolSummary     `json:"tools"`
	Observations []ObservationSummary `json:"observations"`
	Budget       Budget               `json:"budget"`
}

type MCPToolSummary struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Capability  string         `json:"capability"`
}

// PlannerActionKind: tool call vs spawn child master

type PlannerActionKind string

const (
	ActionTool  PlannerActionKind = "tool"
	ActionSpawn PlannerActionKind = "spawn_master"
)

type PlannerAction struct {
	ID         string            `json:"id"`
	Kind       PlannerActionKind `json:"kind"`
	DependsOn  []string          `json:"depends_on,omitempty"`
	Capability string            `json:"capability,omitempty"`
	Tool       string            `json:"tool,omitempty"`
	Args       map[string]any    `json:"args,omitempty"`
	SubGoal    *Topic            `json:"sub_goal,omitempty"`
	Expect     map[string]any    `json:"expect,omitempty"`  // e.g., {"min_chars":1000,"status":200}
	OnFail     map[string]any    `json:"on_fail,omitempty"` // {"retry":2,"fallback_tool":"web.fetch.chromedp"}
	Reason     string            `json:"reason,omitempty"`
}

type PlannerOutput struct {
	Stop    bool            `json:"stop"`
	WhyStop string          `json:"why_stop,omitempty"`
	Actions []PlannerAction `json:"actions"`
}

type Budget struct {
	MaxActions int    `json:"max_actions"`
	ExpiresAt  string `json:"expires_at"`
}
