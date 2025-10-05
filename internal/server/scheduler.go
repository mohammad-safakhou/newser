package server

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/gorhill/cronexpr"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/memory/semantic"
	"github.com/mohammad-safakhou/newser/internal/store"
	"github.com/redis/go-redis/v9"
)

type Scheduler struct {
	store            *store.Store
	stop             chan struct{}
	rdb              *redis.Client
	orch             *core.Orchestrator
	cfg              *config.Config
	sem              *semantic.Ingestor
	embed            core.LLMProvider
	episodeTTL       time.Duration
	lastEpisodePrune time.Time
}

func NewScheduler(cfg *config.Config, store *store.Store, rdb *redis.Client, orch *core.Orchestrator, sem *semantic.Ingestor, provider core.LLMProvider) *Scheduler {
	var ttl time.Duration
	if cfg.Memory.Episodic.Enabled && cfg.Memory.Episodic.RetentionDays > 0 {
		ttl = time.Duration(cfg.Memory.Episodic.RetentionDays) * 24 * time.Hour
	}
	return &Scheduler{
		store:      store,
		stop:       make(chan struct{}),
		rdb:        rdb,
		orch:       orch,
		cfg:        cfg,
		sem:        sem,
		embed:      provider,
		episodeTTL: ttl,
	}
}

func (s *Scheduler) Start() {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			select {
			case <-s.stop:
				ticker.Stop()
				return
			case <-ticker.C:
				s.tick()
			}
		}
	}()
}

func (s *Scheduler) tick() {
	ctx := context.Background()
	s.maybePruneEpisodes(ctx)
	topics, err := s.store.ListAllTopics(ctx)
	if err != nil {
		return
	}
	for _, t := range topics {
		last, _ := s.store.LatestRunTime(ctx, t.ID)
		if !isDue(t.ScheduleCron, last) {
			continue
		}

		// Fire run
		// distributed lock to avoid duplicate runs
		if s.rdb != nil {
			lockKey := "sched:lock:" + t.ID
			ok, _ := s.rdb.SetNX(ctx, lockKey, "1", 2*time.Minute).Result()
			if !ok {
				continue
			}
			defer s.rdb.Del(ctx, lockKey)
		}

		runID, err := s.store.CreateRun(ctx, t.ID, "running")
		if err != nil {
			continue
		}

		go func(topic store.Topic, runID string) {
			// jitter to avoid stampedes
			time.Sleep(time.Duration(250+int64(time.Now().UnixNano()%250)) * time.Millisecond)
			if err := processRun(ctx, s.cfg, s.store, s.orch, s.embed, s.sem, topic.ID, topic.UserID, runID); err != nil {
				_ = s.store.FinishRun(ctx, runID, "failed", strPtr(err.Error()))
			}
		}(t, runID)
	}
}

func deriveThoughtContentFromPrefs(prefs map[string]interface{}) string {
	if prefs == nil {
		return "General news brief"
	}
	if s, ok := prefs["context_summary"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if arr, ok := prefs["objectives"].([]interface{}); ok && len(arr) > 0 {
		var out []string
		for _, it := range arr {
			if str, ok := it.(string); ok {
				out = append(out, str)
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "; ")
		}
	}
	// fallback to a generic description based on keys
	var keys []string
	for k := range prefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return "News brief with preferences: " + strings.Join(keys, ", ")
	}
	return "General news brief"
}

// isDue determines if a topic with cronSpec should run now based on last run time.
// Supports "@daily", "@hourly", and standard 5-field cron expressions.
func isDue(cronSpec string, last *time.Time) bool {
	now := time.Now()
	switch cronSpec {
	case "@daily":
		if last == nil {
			return true
		}
		return now.Sub(*last) >= 24*time.Hour
	case "@hourly":
		if last == nil {
			return true
		}
		return now.Sub(*last) >= time.Hour
	default:
		// Try cron expression
		expr, err := cronexpr.Parse(cronSpec)
		if err != nil {
			// Fallback: treat as @daily if invalid
			if last == nil {
				return true
			}
			return now.Sub(*last) >= 24*time.Hour
		}
		base := now
		if last != nil {
			base = *last
		} else {
			// If never run, due now
			return true
		}
		next := expr.Next(base)
		return !next.After(now)
	}
}

func (s *Scheduler) maybePruneEpisodes(ctx context.Context) {
	if s.episodeTTL <= 0 {
		return
	}
	if !s.lastEpisodePrune.IsZero() && time.Since(s.lastEpisodePrune) < 12*time.Hour {
		return
	}
	cutoff := time.Now().Add(-s.episodeTTL)
	deleted, err := s.store.PruneEpisodesBefore(ctx, cutoff)
	if err != nil {
		log.Printf("scheduler: prune episodic memory failed: %v", err)
		return
	}
	s.lastEpisodePrune = time.Now()
	if deleted > 0 {
		log.Printf("scheduler: pruned %d episodic traces older than %s", deleted, s.episodeTTL)
	}
}
