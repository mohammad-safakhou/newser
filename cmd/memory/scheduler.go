package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	memorymanager "github.com/mohammad-safakhou/newser/internal/memory/manager"
	memorysvc "github.com/mohammad-safakhou/newser/internal/memory/service"
	"github.com/mohammad-safakhou/newser/internal/store"
)

type memorySchedulerOptions struct {
	TickInterval    time.Duration
	SummaryInterval time.Duration
	PruneInterval   time.Duration
}

type memoryScheduler struct {
	store             *store.Store
	manager           *memorymanager.Manager
	logger            *log.Logger
	tickInterval      time.Duration
	summaryInterval   time.Duration
	pruneInterval     time.Duration
	retention         time.Duration
	semanticRetention time.Duration
	lastSummary       map[string]time.Time
	lastSummaryMu     sync.Mutex
	lastPrune         time.Time
	episodicEnabled   bool
	semanticEnabled   bool
}

func newMemoryScheduler(cfg config.MemoryConfig, st *store.Store, mgr *memorymanager.Manager, logger *log.Logger, opts memorySchedulerOptions) *memoryScheduler {
	if st == nil || logger == nil {
		return nil
	}
	tick := opts.TickInterval
	if tick <= 0 {
		tick = defaultTickInterval
	}
	summary := opts.SummaryInterval
	if summary < 6*time.Hour {
		summary = 6 * time.Hour
	}
	prune := opts.PruneInterval
	if prune <= 0 {
		prune = 12 * time.Hour
	}
	retention := time.Duration(cfg.Episodic.RetentionDays) * 24 * time.Hour
	semanticRetention := time.Duration(cfg.Semantic.RetentionDays) * 24 * time.Hour
	if semanticRetention <= 0 {
		semanticRetention = retention
	}

	return &memoryScheduler{
		store:             st,
		manager:           mgr,
		logger:            logger,
		tickInterval:      tick,
		summaryInterval:   summary,
		pruneInterval:     prune,
		retention:         retention,
		semanticRetention: semanticRetention,
		lastSummary:       make(map[string]time.Time),
		episodicEnabled:   cfg.Episodic.Enabled,
		semanticEnabled:   cfg.Semantic.Enabled,
	}
}

func (s *memoryScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.loop(ctx)
}

func (s *memoryScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	s.runTick(ctx) // immediate run to avoid waiting for first tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runTick(ctx)
		}
	}
}

func (s *memoryScheduler) runTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if s.episodicEnabled && s.manager != nil {
		s.runSummaries(ctx)
	}
	if (s.episodicEnabled && s.retention > 0) || (s.semanticEnabled && s.semanticRetention > 0 && s.manager != nil) {
		s.runPrune(ctx)
	}
	if s.manager != nil {
		s.emitHealth(ctx)
	}
}

func (s *memoryScheduler) runSummaries(ctx context.Context) {
	topics, err := s.store.ListAllTopics(ctx)
	if err != nil {
		s.logger.Printf("scheduler: list topics failed: %v", err)
		return
	}
	now := time.Now()
	for _, t := range topics {
		if !s.shouldSummarize(t.ID, now) {
			continue
		}
		resp, err := s.manager.Summarize(ctx, memorysvc.SummaryRequest{
			TopicID: t.ID,
			MaxRuns: 10,
		})
		if err != nil {
			s.logger.Printf("scheduler: summarize %s failed: %v", t.ID, err)
			continue
		}
		s.logger.Printf("scheduler: summarized topic %s with %d items", t.ID, len(resp.Items))
		s.recordSummaryRun(t.ID, now)
	}
}

func (s *memoryScheduler) shouldSummarize(topicID string, now time.Time) bool {
	s.lastSummaryMu.Lock()
	defer s.lastSummaryMu.Unlock()
	last, ok := s.lastSummary[topicID]
	if !ok || now.Sub(last) >= s.summaryInterval {
		return true
	}
	return false
}

func (s *memoryScheduler) recordSummaryRun(topicID string, ts time.Time) {
	s.lastSummaryMu.Lock()
	defer s.lastSummaryMu.Unlock()
	s.lastSummary[topicID] = ts
}

func (s *memoryScheduler) runPrune(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	if !s.lastPrune.IsZero() && now.Sub(s.lastPrune) < s.pruneInterval {
		return
	}

	performed := false
	if s.episodicEnabled && s.retention > 0 {
		cutoff := now.Add(-s.retention)
		deleted, err := s.store.PruneEpisodesBefore(ctx, cutoff)
		if err != nil {
			s.logger.Printf("scheduler: pruning episodic memory failed: %v", err)
			return
		}
		performed = true
		if deleted > 0 {
			s.logger.Printf("scheduler: pruned %d episodic traces older than %s", deleted, s.retention)
		}
	}

	if s.semanticEnabled && s.semanticRetention > 0 && s.manager != nil {
		cutoff := now.Add(-s.semanticRetention)
		stats, err := s.manager.PruneSemanticEmbeddings(ctx, cutoff)
		if err != nil {
			s.logger.Printf("scheduler: pruning semantic memory failed: %v", err)
			return
		}
		performed = true
		if stats.Total() > 0 {
			s.logger.Printf("scheduler: pruned %d run embeddings and %d plan embeddings older than %s", stats.RunEmbeddings, stats.PlanEmbeddings, s.semanticRetention)
		}
	}

	if performed {
		s.lastPrune = now
	}
}

func (s *memoryScheduler) emitHealth(ctx context.Context) {
	stats, err := s.store.MemoryHealthStats(ctx, 24*time.Hour)
	if err != nil {
		s.logger.Printf("scheduler: memory health stats failed: %v", err)
		return
	}
	s.manager.RecordHealthSnapshot(ctx, stats)
}
