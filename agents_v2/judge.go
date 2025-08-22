package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ==========================================================
// Judge (verifier) interface
// ==========================================================

type Judge interface {
	Assess(ctx context.Context, action PlannerAction, result map[string]any) (ok bool, reason string)
}

type SimpleJudge struct {
	MinCharsKey   string
	Model, APIKey string
	Temp          float64
}

func (j SimpleJudge) Assess(ctx context.Context, action PlannerAction, result map[string]any) (bool, string) {
	// Deterministic checks first
	if exp, ok := action.Expect["min_chars"]; ok {
		min := toInt(exp, 0)
		chars := 0
		if c, ok := result["chars"].(int); ok {
			chars = c
		}
		if c, ok := result["chars"].(float64); ok {
			chars = int(c)
		}
		if txt, ok := result["text"].(string); ok && chars == 0 {
			chars = len(txt)
		}
		if chars < min {
			return false, fmt.Sprintf("too short: %d < %d", chars, min)
		}
	}
	if exp, ok := action.Expect["status"]; ok {
		want := toInt(exp, 200)
		got := toInt(result["status"], 0)
		if got != 0 && got != want {
			return false, fmt.Sprintf("status %d != %d", got, want)
		}
	}
	// Optionally call LLM judge (kept simple; best-effort)
	if j.APIKey != "" && j.Model != "" {
		body := map[string]any{
			"model": j.Model, "temperature": j.Temp,
			"response_format": map[string]any{"type": "json_object"},
			"messages": []map[string]any{
				{"role": "system", "content": "You are a strict evaluator. Return {\"ok\":bool,\"reason\":string}."},
				{"role": "user", "content": fmt.Sprintf("ACTION:%s\nEXPECT:%s\nRESULT:%s", mustJSON(action), mustJSON(action.Expect), mustJSON(result))},
			},
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+j.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			var raw struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&raw)
			_ = resp.Body.Close()
			if len(raw.Choices) > 0 {
				var m struct {
					Ok     bool   `json:"ok"`
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal([]byte(raw.Choices[0].Message.Content), &m)
				if !m.Ok {
					return false, m.Reason
				}
			}
		}
	}
	return true, "ok"
}
