package server

import (
    "context"
    "log"
    "time"

    "github.com/gorhill/cronexpr"
    "github.com/redis/go-redis/v9"
    "github.com/mohammad-safakhou/newser/internal/agent/config"
    "github.com/mohammad-safakhou/newser/internal/agent/core"
    "github.com/mohammad-safakhou/newser/internal/agent/telemetry"
    "github.com/mohammad-safakhou/newser/internal/store"
)

type Scheduler struct {
    Store *store.Store
    Stop chan struct{}
    Rdb *redis.Client
    Orch *core.Orchestrator
}

func (s *Scheduler) Start() {
    ticker := time.NewTicker(1 * time.Hour)
    go func() {
        for {
            select {
            case <-s.Stop:
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
    topics, err := s.Store.ListAllTopics(ctx)
    if err != nil { return }
    for _, t := range topics {
        last, _ := s.Store.LatestRunTime(ctx, t.ID)
        if !isDue(t.ScheduleCron, last) { continue }

        // Fire run
        // distributed lock to avoid duplicate runs
        if s.Rdb != nil {
            lockKey := "sched:lock:" + t.ID
            ok, _ := s.Rdb.SetNX(ctx, lockKey, "1", 2*time.Minute).Result()
            if !ok { continue }
            defer s.Rdb.Del(ctx, lockKey)
        }

        runID, err := s.Store.CreateRun(ctx, t.ID, "running")
        if err != nil { continue }

        go func(topic store.Topic, runID string) {
            // jitter to avoid stampedes
            time.Sleep(time.Duration(250+int64(time.Now().UnixNano()%250)) * time.Millisecond)
            orch := s.Orch
            if orch == nil {
                cfg, err := config.LoadConfig()
                if err != nil { _ = s.Store.FinishRun(ctx, runID, "failed", strPtr(err.Error())); return }
                tele := telemetry.NewTelemetry(cfg.Telemetry)
                defer tele.Shutdown()
                logger := log.New(log.Writer(), "[SCHED] ", log.LstdFlags)
                var err2 error
                orch, err2 = core.NewOrchestrator(cfg, logger, tele)
                if err2 != nil { _ = s.Store.FinishRun(ctx, runID, "failed", strPtr(err2.Error())); return }
            }
            thought := core.UserThought{ ID: topic.ID, Content: topic.Name, Timestamp: time.Now() }
            _, err = orch.ProcessThought(ctx, thought)
            if err != nil { _ = s.Store.FinishRun(ctx, runID, "failed", strPtr(err.Error())); return }
            _ = s.Store.FinishRun(ctx, runID, "succeeded", nil)
        }(t, runID)
    }
}

// isDue determines if a topic with cronSpec should run now based on last run time.
// Supports "@daily", "@hourly", and standard 5-field cron expressions.
func isDue(cronSpec string, last *time.Time) bool {
    now := time.Now()
    switch cronSpec {
    case "@daily":
        if last == nil { return true }
        return now.Sub(*last) >= 24*time.Hour
    case "@hourly":
        if last == nil { return true }
        return now.Sub(*last) >= time.Hour
    default:
        // Try cron expression
        expr, err := cronexpr.Parse(cronSpec)
        if err != nil {
            // Fallback: treat as @daily if invalid
            if last == nil { return true }
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


