package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file")
	runID := flag.String("run", "", "run identifier to inspect")
	asJSON := flag.Bool("json", false, "emit full episodic snapshot as JSON")
	showSteps := flag.Bool("steps", false, "print per-step details")
	flag.Parse()

	if *runID == "" {
		log.Fatal("replay requires --run <run_id>")
	}

	cfg := config.LoadConfig(*cfgPath)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dsn, err := runtime.BuildPostgresDSN(cfg)
	if err != nil {
		log.Fatalf("build dsn: %v", err)
	}
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		log.Fatalf("connect store: %v", err)
	}

	episode, ok, err := st.GetEpisodeByRunID(ctx, *runID)
	if err != nil {
		log.Fatalf("load episode: %v", err)
	}
	if !ok {
		log.Fatalf("no episode found for run %s", *runID)
	}

	if *asJSON {
		resp := newEpisodeResponse(episode)
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			log.Fatalf("marshal episode: %v", err)
		}
		os.Stdout.Write(data)
		os.Stdout.Write([]byte("\n"))
		return
	}

	printEpisodeSummary(episode)
	if *showSteps {
		fmt.Println()
		printEpisodeSteps(episode)
	}
}

func printEpisodeSummary(ep store.Episode) {
	fmt.Printf("Run: %s\nTopic: %s\nRecorded: %s\n", ep.RunID, ep.TopicID, ep.CreatedAt.Format(time.RFC3339))
	fmt.Println()
	if ep.PlanPrompt != "" {
		fmt.Println("Plan Prompt:")
		fmt.Println(strings.TrimSpace(ep.PlanPrompt))
		fmt.Println()
	}
	fmt.Println("Result Summary:")
	fmt.Println(strings.TrimSpace(ep.Result.Summary))
	fmt.Println()
	fmt.Printf("Highlights recorded: %d\n", len(ep.Result.Highlights))
	fmt.Printf("Steps executed: %d\n", len(ep.Steps))
}

func printEpisodeSteps(ep store.Episode) {
	for _, step := range ep.Steps {
		fmt.Printf("Step %d — %s (%s)\n", step.StepIndex, step.Task.ID, step.Result.AgentType)
		if step.Prompt != "" {
			snippet := step.Prompt
			if len(snippet) > 160 {
				snippet = snippet[:160] + "…"
			}
			fmt.Printf("  Prompt: %s\n", snippet)
		}
		if step.Result.Error != "" {
			fmt.Printf("  Error: %s\n", step.Result.Error)
		} else {
			fmt.Printf("  Success: %t, Cost: %.2f, Tokens: %d\n", step.Result.Success, step.Result.Cost, step.Result.TokensUsed)
		}
		if len(step.Artifacts) > 0 {
			fmt.Printf("  Artifacts: %d\n", len(step.Artifacts))
		}
		fmt.Println()
	}
}

func newEpisodeResponse(ep store.Episode) episodeResponse {
	resp := episodeResponse{
		RunID:        ep.RunID,
		TopicID:      ep.TopicID,
		Thought:      ep.Thought,
		PlanDocument: ep.PlanDocument,
		PlanPrompt:   ep.PlanPrompt,
		Result:       ep.Result,
		CreatedAt:    ep.CreatedAt,
	}
	if len(ep.PlanRaw) > 0 {
		resp.PlanRaw = append(resp.PlanRaw, ep.PlanRaw...)
	}
	if len(ep.Steps) > 0 {
		resp.Steps = make([]episodeStepResponse, len(ep.Steps))
		for i, step := range ep.Steps {
			resp.Steps[i] = episodeStepResponse{
				StepIndex:     step.StepIndex,
				Task:          step.Task,
				InputSnapshot: step.InputSnapshot,
				Prompt:        step.Prompt,
				Result:        step.Result,
				Artifacts:     step.Artifacts,
				StartedAt:     step.StartedAt,
				CompletedAt:   step.CompletedAt,
				CreatedAt:     step.CreatedAt,
			}
		}
	}
	return resp
}

type episodeResponse struct {
	RunID        string                     `json:"run_id"`
	TopicID      string                     `json:"topic_id"`
	Thought      agentcore.UserThought      `json:"thought"`
	PlanDocument *planner.PlanDocument      `json:"plan_document,omitempty"`
	PlanRaw      json.RawMessage            `json:"plan_raw,omitempty"`
	PlanPrompt   string                     `json:"plan_prompt,omitempty"`
	Result       agentcore.ProcessingResult `json:"result"`
	Steps        []episodeStepResponse      `json:"steps"`
	CreatedAt    time.Time                  `json:"created_at"`
}

type episodeStepResponse struct {
	StepIndex     int                      `json:"step_index"`
	Task          agentcore.AgentTask      `json:"task"`
	InputSnapshot map[string]interface{}   `json:"input_snapshot,omitempty"`
	Prompt        string                   `json:"prompt,omitempty"`
	Result        agentcore.AgentResult    `json:"result"`
	Artifacts     []map[string]interface{} `json:"artifacts,omitempty"`
	StartedAt     *time.Time               `json:"started_at,omitempty"`
	CompletedAt   *time.Time               `json:"completed_at,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
}
