package manager

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

// Manager provides a lightweight episodic memory implementation backed by the primary store.
type Manager struct {
	store  *store.Store
	cfg    config.MemoryConfig
	logger *log.Logger
}

// New constructs a memory manager when episodic memory is enabled.
func New(st *store.Store, cfg config.MemoryConfig, logger *log.Logger) *Manager {
	if st == nil {
		return nil
	}
	if !cfg.Episodic.Enabled {
		return nil
	}
	if logger == nil {
		logger = log.New(log.Writer(), "[MEMORY] ", log.LstdFlags)
	}
	return &Manager{store: st, cfg: cfg, logger: logger}
}

// WriteEpisode persists an episodic snapshot for later replay and summarisation.
func (m *Manager) WriteEpisode(ctx context.Context, snapshot agentcore.EpisodicSnapshot) error {
	if m == nil {
		return fmt.Errorf("memory manager unavailable")
	}
	ep, err := snapshotToEpisode(snapshot)
	if err != nil {
		return err
	}
	if err := m.store.SaveEpisode(ctx, ep); err != nil {
		return fmt.Errorf("save episode: %w", err)
	}
	return nil
}

// Summarize aggregates recent run summaries for a topic.
func (m *Manager) Summarize(ctx context.Context, req memorysvc.SummaryRequest) (memorysvc.SummaryResponse, error) {
	if m == nil {
		return memorysvc.SummaryResponse{}, fmt.Errorf("memory manager unavailable")
	}
	if strings.TrimSpace(req.TopicID) == "" {
		return memorysvc.SummaryResponse{}, fmt.Errorf("topic_id required")
	}
	maxRuns := req.MaxRuns
	if maxRuns <= 0 {
		maxRuns = 5
	}
	if maxRuns > 20 {
		maxRuns = 20
	}

	runs, err := m.store.ListRuns(ctx, req.TopicID)
	if err != nil {
		return memorysvc.SummaryResponse{}, fmt.Errorf("list runs: %w", err)
	}

	resp := memorysvc.SummaryResponse{TopicID: req.TopicID, GeneratedAt: time.Now().UTC()}
	limit := maxRuns
	if len(runs) < limit {
		limit = len(runs)
	}
	var lines []string
	for i := 0; i < limit; i++ {
		run := runs[i]
		episode, ok, err := m.store.GetEpisodeByRunID(ctx, run.ID)
		if err != nil {
			return memorysvc.SummaryResponse{}, fmt.Errorf("get episode %s: %w", run.ID, err)
		}
		if !ok {
			continue
		}
		summary := strings.TrimSpace(episode.Result.Summary)
		if summary == "" {
			summary = deriveFallbackSummary(episode.Result)
		}
		item := memorysvc.SummaryItem{
			RunID:     run.ID,
			Summary:   summary,
			StartedAt: run.StartedAt,
		}
		if run.FinishedAt != nil {
			item.FinishedAt = run.FinishedAt
		}
		resp.Items = append(resp.Items, item)
		if summary != "" {
			lines = append(lines, "- "+summary)
		}
	}
	resp.Summary = strings.Join(lines, "\n")
	return resp, nil
}

// Delta filters already-seen items using known identifiers or hashes supplied by the caller.
func (m *Manager) Delta(_ context.Context, req memorysvc.DeltaRequest) (memorysvc.DeltaResponse, error) {
	if m == nil {
		return memorysvc.DeltaResponse{}, fmt.Errorf("memory manager unavailable")
	}
	if strings.TrimSpace(req.TopicID) == "" {
		return memorysvc.DeltaResponse{}, fmt.Errorf("topic_id required")
	}
	known := make(map[string]struct{}, len(req.KnownIDs))
	for _, id := range req.KnownIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		known[id] = struct{}{}
	}
	seen := make(map[string]struct{})
	resp := memorysvc.DeltaResponse{TopicID: req.TopicID, Total: len(req.Items)}
	for _, item := range req.Items {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = strings.TrimSpace(item.Hash)
		}
		if key == "" {
			continue
		}
		if _, ok := known[key]; ok {
			resp.DuplicateCount++
			continue
		}
		if _, ok := seen[key]; ok {
			resp.DuplicateCount++
			continue
		}
		seen[key] = struct{}{}
		resp.Novel = append(resp.Novel, item)
	}
	return resp, nil
}

func deriveFallbackSummary(res agentcore.ProcessingResult) string {
	detailed := strings.TrimSpace(res.DetailedReport)
	if detailed == "" {
		return ""
	}
	if idx := strings.IndexRune(detailed, '\n'); idx > 0 {
		detailed = detailed[:idx]
	}
	if len(detailed) > 320 {
		detailed = detailed[:320]
	}
	return detailed
}

func snapshotToEpisode(snapshot agentcore.EpisodicSnapshot) (store.Episode, error) {
	if strings.TrimSpace(snapshot.RunID) == "" {
		return store.Episode{}, fmt.Errorf("run_id required")
	}
	if strings.TrimSpace(snapshot.TopicID) == "" {
		return store.Episode{}, fmt.Errorf("topic_id required")
	}
	if strings.TrimSpace(snapshot.UserID) == "" {
		return store.Episode{}, fmt.Errorf("user_id required")
	}
	ep := store.Episode{
		RunID:        snapshot.RunID,
		TopicID:      snapshot.TopicID,
		UserID:       snapshot.UserID,
		Thought:      snapshot.Thought,
		PlanDocument: snapshot.PlanDocument,
		PlanPrompt:   snapshot.PlanPrompt,
		Result:       snapshot.Result,
	}
	if len(snapshot.PlanRaw) > 0 {
		ep.PlanRaw = append([]byte(nil), snapshot.PlanRaw...)
	}
	if len(snapshot.Steps) > 0 {
		steps := make([]store.EpisodeStep, len(snapshot.Steps))
		for i, step := range snapshot.Steps {
			steps[i] = store.EpisodeStep{
				StepIndex:     step.StepIndex,
				Task:          step.Task,
				InputSnapshot: step.InputSnapshot,
				Prompt:        step.Prompt,
				Result:        step.Result,
				Artifacts:     step.Artifacts,
			}
			if !step.StartedAt.IsZero() {
				started := step.StartedAt
				steps[i].StartedAt = &started
			}
			if !step.CompletedAt.IsZero() {
				completed := step.CompletedAt
				steps[i].CompletedAt = &completed
			}
		}
		ep.Steps = steps
	}
	return ep, nil
}

// Ensure Manager satisfies the service.Manager interface.
var _ memorysvc.Manager = (*Manager)(nil)

// SnapshotToEpisode converts an episodic snapshot into the store representation.
func SnapshotToEpisode(snapshot agentcore.EpisodicSnapshot) (store.Episode, error) {
	return snapshotToEpisode(snapshot)
}
