package server

import (
	"context"
	"encoding/json"
	"fmt"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type episodicRepository struct {
	store *store.Store
}

func newEpisodicRepository(st *store.Store) agentcore.EpisodeRepository {
	if st == nil {
		return nil
	}
	return &episodicRepository{store: st}
}

func (r *episodicRepository) SaveEpisode(ctx context.Context, snapshot agentcore.EpisodicSnapshot) error {
	if snapshot.RunID == "" || snapshot.TopicID == "" || snapshot.UserID == "" {
		return fmt.Errorf("episodic snapshot missing identifiers")
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
		ep.PlanRaw = append(json.RawMessage{}, snapshot.PlanRaw...)
	}

	if len(snapshot.Steps) > 0 {
		ep.Steps = make([]store.EpisodeStep, len(snapshot.Steps))
		for i, step := range snapshot.Steps {
			ep.Steps[i] = store.EpisodeStep{
				StepIndex:     step.StepIndex,
				Task:          step.Task,
				InputSnapshot: step.InputSnapshot,
				Prompt:        step.Prompt,
				Result:        step.Result,
				Artifacts:     step.Artifacts,
			}
			if !step.StartedAt.IsZero() {
				started := step.StartedAt
				ep.Steps[i].StartedAt = &started
			}
			if !step.CompletedAt.IsZero() {
				completed := step.CompletedAt
				ep.Steps[i].CompletedAt = &completed
			}
		}
	}

	return r.store.SaveEpisode(ctx, ep)
}
