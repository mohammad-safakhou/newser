package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/planner"
	"github.com/mohammad-safakhou/newser/internal/store"
)

const defaultRebuildBatchSize = 100

func rebuildSemanticEmbeddings(ctx context.Context, st *store.Store, ing *semantic.Ingestor, batchSize int, logger *log.Logger) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("context is nil")
	}
	if st == nil {
		return 0, fmt.Errorf("store is nil")
	}
	if ing == nil {
		return 0, fmt.Errorf("semantic ingestor is nil")
	}
	if batchSize <= 0 {
		batchSize = defaultRebuildBatchSize
	}
	if batchSize > 200 {
		batchSize = 200
	}
	processed := 0
	var cursor time.Time

	for {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}

		filter := store.EpisodeFilter{Limit: batchSize}
		if !cursor.IsZero() {
			filter.To = cursor
		}
		episodes, err := st.ListEpisodes(ctx, filter)
		if err != nil {
			return processed, fmt.Errorf("list episodes: %w", err)
		}
		if len(episodes) == 0 {
			break
		}

		for _, summary := range episodes {
			select {
			case <-ctx.Done():
				return processed, ctx.Err()
			default:
			}
			if summary.RunID == "" || summary.TopicID == "" {
				continue
			}
			episode, ok, err := st.GetEpisodeByRunID(ctx, summary.RunID)
			if err != nil {
				return processed, fmt.Errorf("load episode %s: %w", summary.RunID, err)
			}
			if !ok {
				continue
			}

			planDoc := episode.PlanDocument
			if planDoc == nil && len(episode.PlanRaw) > 0 {
				if doc, _, err := planner.NormalizePlanDocument([]byte(episode.PlanRaw)); err != nil {
					if logger != nil {
						logger.Printf("skip plan normalisation for run %s: %v", episode.RunID, err)
					}
				} else {
					planDoc = doc
				}
			}

			if err := ing.IngestRun(ctx, episode.TopicID, episode.RunID, episode.Result, planDoc); err != nil {
				return processed, fmt.Errorf("ingest run %s: %w", episode.RunID, err)
			}
			processed++
			if logger != nil && processed%50 == 0 {
				logger.Printf("semantic rebuild processed %d runs", processed)
			}
		}

		last := episodes[len(episodes)-1].CreatedAt
		cursor = last.Add(-time.Nanosecond)
		if cursor.Before(time.Unix(0, 0)) {
			cursor = time.Unix(0, 0)
		}
	}

	if logger != nil {
		logger.Printf("semantic rebuild finished, processed %d runs", processed)
	}
	return processed, nil
}
