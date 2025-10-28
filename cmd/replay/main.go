package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/runtime"
	"github.com/mohammad-safakhou/newser/internal/store"
)

const defaultConfigPath = "config/config.json"

func main() {
	if len(os.Args) < 2 {
		printRootUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "show":
		err = runShowCommand(args)
	case "list":
		err = runListCommand(args)
	case "export-plan":
		err = runExportPlanCommand(args)
	case "diff":
		err = runDiffCommand(args)
	default:
		printRootUsage()
		os.Exit(1)
	}

	if err != nil {
		log.Fatal(err)
	}
}

func printRootUsage() {
	fmt.Println(`Usage: replay <command> [options]

Commands:
  show         Display a stored episode (with optional JSON output).
  list         List recorded episodes with optional filters.
  export-plan  Export the stored plan/context for deterministic re-execution.
  diff         Compare a stored episode against a replay JSON export.
`)
}

func runShowCommand(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	runID := fs.String("run", "", "run identifier to inspect")
	jsonOut := fs.Bool("json", false, "emit full episodic snapshot as JSON")
	showSteps := fs.Bool("steps", false, "print per-step details")
	showArtifacts := fs.Bool("artifacts", false, "include artifact payloads when printing steps")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		return errors.New("show requires --run <run_id>")
	}

	return withStore(*cfgPath, func(ctx context.Context, st *store.Store, cfg *config.Config) error {
		episode, ok, err := st.GetEpisodeByRunID(ctx, *runID)
		if err != nil {
			return fmt.Errorf("load episode: %w", err)
		}
		if !ok {
			return fmt.Errorf("no episode found for run %s", *runID)
		}
		planDoc := planWithMetadata(episode)

		status, err := verifyRunManifest(ctx, st, cfg, episode.RunID)
		if err != nil {
			return fmt.Errorf("run manifest verification failed: %w", err)
		}
		logManifestStatus(status, episode.RunID)

		summaries, err := st.ListEpisodes(ctx, store.EpisodeFilter{RunID: *runID, Limit: 1})
		if err != nil {
			return fmt.Errorf("episode summary: %w", err)
		}
		var status string
		var startedAt *time.Time
		if len(summaries) > 0 {
			status = summaries[0].Status
			startedAt = summaries[0].StartedAt
		}

		if *jsonOut {
			resp := newEpisodeResponse(episode)
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal episode: %w", err)
			}
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			return nil
		}

		printEpisodeSummary(episode, status, startedAt)
		if planDoc != nil {
			printProceduralTemplates(extractProceduralTemplateUsage(planDoc))
		}
		if *showSteps {
			fmt.Println()
			printEpisodeSteps(episode, *showArtifacts)
		}
		return nil
	})
}

func runListCommand(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	topicID := fs.String("topic", "", "filter by topic identifier")
	status := fs.String("status", "", "filter by run status (pending, running, completed, failed)")
	fromStr := fs.String("from", "", "filter episodes created on/after RFC3339 timestamp")
	toStr := fs.String("to", "", "filter episodes created on/before RFC3339 timestamp")
	limit := fs.Int("limit", 20, "maximum number of episodes to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return withStore(*cfgPath, func(ctx context.Context, st *store.Store, cfg *config.Config) error {
		filter := store.EpisodeFilter{TopicID: *topicID, Status: strings.ToLower(*status), Limit: *limit}
		if *fromStr != "" {
			ts, err := time.Parse(time.RFC3339, *fromStr)
			if err != nil {
				return fmt.Errorf("parse --from: %w", err)
			}
			filter.From = ts
		}
		if *toStr != "" {
			ts, err := time.Parse(time.RFC3339, *toStr)
			if err != nil {
				return fmt.Errorf("parse --to: %w", err)
			}
			filter.To = ts
		}

		episodes, err := st.ListEpisodes(ctx, filter)
		if err != nil {
			return fmt.Errorf("list episodes: %w", err)
		}

		if len(episodes) == 0 {
			fmt.Println("(no episodes found)")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "RUN ID\tTOPIC\tSTATUS\tCREATED\tSTEPS")
		for _, ep := range episodes {
			created := ep.CreatedAt.Format(time.RFC3339)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", ep.RunID, ep.TopicID, displayStatus(ep.Status), created, ep.Steps)
		}
		tw.Flush()
		return nil
	})
}

func runExportPlanCommand(args []string) error {
	fs := flag.NewFlagSet("export-plan", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	runID := fs.String("run", "", "run identifier")
	outPath := fs.String("out", "", "destination file (stdout if omitted)")
	includeContext := fs.Bool("include-context", true, "include thought preferences/context in export")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		return errors.New("export-plan requires --run <run_id>")
	}

	return withStore(*cfgPath, func(ctx context.Context, st *store.Store, cfg *config.Config) error {
		episode, ok, err := st.GetEpisodeByRunID(ctx, *runID)
		if err != nil {
			return fmt.Errorf("load episode: %w", err)
		}
		if !ok {
			return fmt.Errorf("no episode found for run %s", *runID)
		}
		if episode.PlanDocument == nil && len(episode.PlanRaw) == 0 {
			return fmt.Errorf("no stored plan for run %s", *runID)
		}

		export := planExport{
			RunID:   episode.RunID,
			TopicID: episode.TopicID,
			UserID:  episode.UserID,
		}
		if episode.PlanDocument != nil {
			export.Plan = *episode.PlanDocument
		} else if len(episode.PlanRaw) > 0 {
			var doc planner.PlanDocument
			if err := json.Unmarshal(episode.PlanRaw, &doc); err != nil {
				return fmt.Errorf("unmarshal stored plan: %w", err)
			}
			export.Plan = doc
		}
		if len(episode.PlanRaw) > 0 {
			export.PlanRaw = append(export.PlanRaw, episode.PlanRaw...)
		}
		if *includeContext {
			export.Thought = episode.Thought
		}

		data, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal export: %w", err)
		}

		if *outPath == "" {
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
			return fmt.Errorf("create export dir: %w", err)
		}
		if err := os.WriteFile(*outPath, data, 0o600); err != nil {
			return fmt.Errorf("write export: %w", err)
		}
		fmt.Printf("Plan written to %s\n", *outPath)
		return nil
	})
}

func runDiffCommand(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config file")
	runID := fs.String("run", "", "run identifier")
	replayPath := fs.String("file", "", "JSON export produced by 'replay show --json'")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		return errors.New("diff requires --run <run_id>")
	}
	if *replayPath == "" {
		return errors.New("diff requires --file <replay.json>")
	}

	candidateBytes, err := os.ReadFile(*replayPath)
	if err != nil {
		return fmt.Errorf("read replay file: %w", err)
	}
	var candidate episodeResponse
	if err := json.Unmarshal(candidateBytes, &candidate); err != nil {
		return fmt.Errorf("parse replay file: %w", err)
	}

	return withStore(*cfgPath, func(ctx context.Context, st *store.Store, cfg *config.Config) error {
		episode, ok, err := st.GetEpisodeByRunID(ctx, *runID)
		if err != nil {
			return fmt.Errorf("load episode: %w", err)
		}
		if !ok {
			return fmt.Errorf("no episode found for run %s", *runID)
		}

		status, err := verifyRunManifest(ctx, st, cfg, episode.RunID)
		if err != nil {
			return fmt.Errorf("run manifest verification failed: %w", err)
		}
		logManifestStatus(status, episode.RunID)

		orig := newEpisodeResponse(episode)
		diffs := diffEpisodeResponses(orig, candidate)
		if len(diffs) == 0 {
			fmt.Println("No differences detected.")
			return nil
		}
		fmt.Println("Differences detected:")
		for _, d := range diffs {
			fmt.Printf("- %s\n", d)
		}
		return nil
	})
}

func withStore(cfgPath string, fn func(context.Context, *store.Store, *config.Config) error) error {
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}
	cfg := config.LoadConfig(cfgPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn, err := runtime.BuildPostgresDSN(cfg)
	if err != nil {
		return err
	}
	st, err := store.NewWithDSN(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.DB.Close()

	return fn(ctx, st, cfg)
}

func printEpisodeSummary(ep store.Episode, status string, startedAt *time.Time) {
	fmt.Printf("Run: %s\nTopic: %s\n", ep.RunID, ep.TopicID)
	if status != "" {
		fmt.Printf("Status: %s\n", displayStatus(status))
	}
	if startedAt != nil {
		fmt.Printf("Started: %s\n", startedAt.Format(time.RFC3339))
	}
	fmt.Printf("Recorded: %s\n\n", ep.CreatedAt.Format(time.RFC3339))

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

func printEpisodeSteps(ep store.Episode, includeArtifacts bool) {
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
			if includeArtifacts {
				for idx, artifact := range step.Artifacts {
					payload, err := json.MarshalIndent(artifact, "    ", "  ")
					if err != nil {
						fmt.Printf("    [%d] <error marshaling artifact>\n", idx)
						continue
					}
					fmt.Printf("    [%d] %s\n", idx, string(payload))
				}
			}
		}
		fmt.Println()
	}
}

func displayStatus(status string) string {
	if status == "" {
		return "unknown"
	}
	return status
}

func newEpisodeResponse(ep store.Episode) episodeResponse {
	resp := episodeResponse{
		RunID:      ep.RunID,
		TopicID:    ep.TopicID,
		Thought:    ep.Thought,
		PlanPrompt: ep.PlanPrompt,
		Result:     ep.Result,
		CreatedAt:  ep.CreatedAt,
	}
	if plan := planWithMetadata(ep); plan != nil {
		resp.PlanDocument = plan
		resp.ProceduralTemplates = extractProceduralTemplateUsage(plan)
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

func diffEpisodeResponses(orig, candidate episodeResponse) []string {
	var diffs []string
	if strings.TrimSpace(orig.Result.Summary) != strings.TrimSpace(candidate.Result.Summary) {
		diffs = append(diffs, fmt.Sprintf("result summary changed: %q → %q", orig.Result.Summary, candidate.Result.Summary))
	}
	if orig.Result.CostEstimate != candidate.Result.CostEstimate {
		diffs = append(diffs, fmt.Sprintf("result cost estimate changed: %.2f → %.2f", orig.Result.CostEstimate, candidate.Result.CostEstimate))
	}
	if orig.Result.TokensUsed != candidate.Result.TokensUsed {
		diffs = append(diffs, fmt.Sprintf("result tokens used changed: %d → %d", orig.Result.TokensUsed, candidate.Result.TokensUsed))
	}

	origSteps := make(map[int]episodeStepResponse, len(orig.Steps))
	for _, step := range orig.Steps {
		origSteps[step.StepIndex] = step
	}
	candidateSteps := make(map[int]episodeStepResponse, len(candidate.Steps))
	for _, step := range candidate.Steps {
		candidateSteps[step.StepIndex] = step
	}

	for index, origStep := range origSteps {
		candStep, ok := candidateSteps[index]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("step %d missing in candidate replay", index))
			continue
		}
		if origStep.Result.Success != candStep.Result.Success {
			diffs = append(diffs, fmt.Sprintf("step %d success changed: %t → %t", index, origStep.Result.Success, candStep.Result.Success))
		}
		if origStep.Result.Cost != candStep.Result.Cost {
			diffs = append(diffs, fmt.Sprintf("step %d cost changed: %.2f → %.2f", index, origStep.Result.Cost, candStep.Result.Cost))
		}
		if origStep.Result.TokensUsed != candStep.Result.TokensUsed {
			diffs = append(diffs, fmt.Sprintf("step %d tokens used changed: %d → %d", index, origStep.Result.TokensUsed, candStep.Result.TokensUsed))
		}
		if strings.TrimSpace(origStep.Result.Error) != strings.TrimSpace(candStep.Result.Error) {
			diffs = append(diffs, fmt.Sprintf("step %d error changed: %q → %q", index, origStep.Result.Error, candStep.Result.Error))
		}
	}

	for index := range candidateSteps {
		if _, ok := origSteps[index]; !ok {
			diffs = append(diffs, fmt.Sprintf("step %d present only in candidate replay", index))
		}
	}

	return diffs
}

type episodeResponse struct {
	RunID               string                     `json:"run_id"`
	TopicID             string                     `json:"topic_id"`
	Thought             agentcore.UserThought      `json:"thought"`
	PlanDocument        *planner.PlanDocument      `json:"plan_document,omitempty"`
	PlanRaw             json.RawMessage            `json:"plan_raw,omitempty"`
	PlanPrompt          string                     `json:"plan_prompt,omitempty"`
	ProceduralTemplates []proceduralTemplateUsage  `json:"procedural_templates,omitempty"`
	Result              agentcore.ProcessingResult `json:"result"`
	Steps               []episodeStepResponse      `json:"steps"`
	CreatedAt           time.Time                  `json:"created_at"`
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

type planExport struct {
	RunID   string                `json:"run_id"`
	TopicID string                `json:"topic_id"`
	UserID  string                `json:"user_id"`
	Thought agentcore.UserThought `json:"thought,omitempty"`
	Plan    planner.PlanDocument  `json:"plan"`
	PlanRaw json.RawMessage       `json:"plan_raw,omitempty"`
}

type proceduralTemplateUsage struct {
	Stage       string   `json:"stage"`
	Status      string   `json:"status"`
	TemplateID  string   `json:"template_id,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	TaskCount   int      `json:"task_count"`
	TaskIDs     []string `json:"task_ids,omitempty"`
	TaskTypes   []string `json:"task_types,omitempty"`
	Occurrences int      `json:"occurrences"`
}

func planWithMetadata(ep store.Episode) *planner.PlanDocument {
	if ep.PlanDocument != nil {
		return ep.PlanDocument
	}
	if len(ep.PlanRaw) == 0 {
		return nil
	}
	var doc planner.PlanDocument
	if err := json.Unmarshal(ep.PlanRaw, &doc); err != nil {
		return nil
	}
	return &doc
}

func extractProceduralTemplateUsage(plan *planner.PlanDocument) []proceduralTemplateUsage {
	if plan == nil || plan.Metadata == nil {
		return nil
	}
	raw, ok := plan.Metadata["procedural_templates"]
	if !ok {
		return nil
	}
	var entries []map[string]interface{}
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			entries = append(entries, m)
		}
	case []map[string]interface{}:
		entries = append(entries, v...)
	default:
		return nil
	}
	usages := make([]proceduralTemplateUsage, 0, len(entries))
	for _, entry := range entries {
		usage := proceduralTemplateUsage{
			Stage:       strings.TrimSpace(stringFromAny(entry["stage"])),
			Status:      strings.TrimSpace(stringFromAny(entry["status"])),
			TemplateID:  strings.TrimSpace(stringFromAny(entry["template_id"])),
			Fingerprint: strings.TrimSpace(stringFromAny(entry["fingerprint"])),
			Occurrences: intFromAny(entry["occurrences"]),
		}
		if usage.Status == "" {
			usage.Status = "candidate"
		}
		usage.TaskIDs = stringsFromAny(entry["task_ids"])
		usage.TaskCount = len(usage.TaskIDs)
		usage.TaskTypes = stringsFromAny(entry["task_types"])
		usages = append(usages, usage)
	}
	sort.Slice(usages, func(i, j int) bool {
		if usages[i].Stage == usages[j].Stage {
			if usages[i].Status == usages[j].Status {
				return usages[i].TemplateID < usages[j].TemplateID
			}
			return usages[i].Status < usages[j].Status
		}
		return usages[i].Stage < usages[j].Stage
	})
	return usages
}

func printProceduralTemplates(usages []proceduralTemplateUsage) {
	if len(usages) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Procedural Templates:")
	for _, usage := range usages {
		line := fmt.Sprintf("  - Stage %s | Status: %s", usage.Stage, usage.Status)
		if usage.TemplateID != "" {
			line += fmt.Sprintf(" | Template: %s", usage.TemplateID)
		}
		if usage.Fingerprint != "" {
			line += fmt.Sprintf(" | Fingerprint: %s", shortFingerprint(usage.Fingerprint))
		}
		if usage.Occurrences > 0 {
			line += fmt.Sprintf(" | Occurrences: %d", usage.Occurrences)
		}
		line += fmt.Sprintf(" | Tasks: %d", usage.TaskCount)
		if usage.TaskCount > 0 && usage.TaskCount <= 5 {
			line += fmt.Sprintf(" (%s)", strings.Join(usage.TaskIDs, ", "))
		}
		fmt.Println(line)
	}
}

func stringFromAny(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return ""
	}
}

func stringsFromAny(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str := strings.TrimSpace(stringFromAny(item))
			if str != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func intFromAny(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	default:
		return 0
	}
	return 0
}

func shortFingerprint(fp string) string {
	if len(fp) <= 8 {
		return fp
	}
	return fp[:8]
}
