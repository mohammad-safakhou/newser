package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ==========================================================
// Events / Repo / Sink
// ==========================================================

type Stage string

const (
	StageKickoff  Stage = "kickoff"
	StagePlan     Stage = "plan"
	StageAction   Stage = "action"
	StageVerify   Stage = "verify"
	StageSpawn    Stage = "spawn"
	StageComplete Stage = "complete"
	StageError    Stage = "error"
)

type StreamItem struct {
	RunID string    `json:"run_id"`
	Node  string    `json:"node"`
	Stage Stage     `json:"stage"`
	Title string    `json:"title,omitempty"`
	URL   string    `json:"url,omitempty"`
	Note  string    `json:"note,omitempty"`
	Err   string    `json:"err,omitempty"`
	Time  time.Time `json:"time"`
}

type AgentRunRepository interface {
	Save(ctx context.Context, s StreamItem) error
	List(ctx context.Context, runID string) ([]StreamItem, error)
}

type InMemoryRepo struct {
	mu   sync.Mutex
	data map[string][]StreamItem
}

func NewInMemoryRepo() *InMemoryRepo { return &InMemoryRepo{data: map[string][]StreamItem{}} }

func (r *InMemoryRepo) Save(_ context.Context, s StreamItem) error {
	r.mu.Lock()
	r.data[s.RunID] = append(r.data[s.RunID], s)
	r.mu.Unlock()
	return nil
}
func (r *InMemoryRepo) List(_ context.Context, runID string) ([]StreamItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]StreamItem, len(r.data[runID]))
	copy(cp, r.data[runID])
	return cp, nil
}

type FileRunSink struct{ dir string }

func NewFileRunSink(dir string) (*FileRunSink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileRunSink{dir: dir}, nil
}

type RunSummary struct {
	RunID   string       `json:"run_id"`
	Items   []StreamItem `json:"items"`
	Report  string       `json:"report,omitempty"`
	EndedAt time.Time    `json:"ended_at"`
}

func (s *FileRunSink) Save(ctx context.Context, runID string, items []StreamItem) error {
	b, _ := json.MarshalIndent(RunSummary{RunID: runID, Items: items, EndedAt: time.Now()}, "", "  ")
	return os.WriteFile(fmt.Sprintf("%s/%s.json", s.dir, runID), b, 0o644)
}
