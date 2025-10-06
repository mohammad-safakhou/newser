package server

import (
	"context"
	"fmt"

	agentcore "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/memory/manager"
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
	ep, err := manager.SnapshotToEpisode(snapshot)
	if err != nil {
		return fmt.Errorf("convert snapshot: %w", err)
	}
	return r.store.SaveEpisode(ctx, ep)
}
