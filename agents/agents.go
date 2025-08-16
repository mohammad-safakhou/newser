// newser_master_agents_singlefile.go
// Single-file, modular Master–Agents layer with MCP integration for newser
//
// Design goals in this version:
//   - **MCP-first**: All operational steps (discover, fetch, ingest, query, embed)
//     are executed via MCP tools. We only fall back to local logic if explicitly
//     configured, and we explain why (see AppConfig.AllowLocalFallback).
//   - **Dynamic but readable**: Capabilities and tool names are configured via
//     small constants/maps so you can control behavior without hunting through code.
//   - **Observability**: Every function logs meaningful, structured events.
//   - **Safety**: Robust stdio JSON-RPC handling, bounded frames, serialized I/O,
//     timeouts on tool calls, and a run tracker that detects completion.
//   - **Docs**: Each exported function includes a docstring explaining purpose
//     and tricky bits.
//
// How it works at a glance:
//   1) Master spawns one or more MCP agents (processes) from env vars.
//   2) Each agent is introspected for tools; we map tool names -> capabilities.
//   3) Planner converts a Goal into tasks. Workers execute tasks by selecting an
//      agent that can satisfy the capability and calling an MCP tool.
//   4) Streamed events go to stdout and a per-run JSON artifact in ./runs/<id>.json.
//
// Minimal env to run (one MCP binary exposing all tools is fine):
//   MCP_SEARCH_CMD=./bin/newser-mcp
//   MCP_FETCH_CMD=./bin/newser-mcp
//   MCP_INGEST_CMD=./bin/newser-mcp   (optional; falls back to SEARCH/FETCH agent if unset)
//
// Notes about fallbacks:
//   - If your MCP server **already exposes** `web.ingest`, `search.query`, and
//     `embedding.embed_many`, we never need local fallbacks.
//   - If a capability is missing on any agent and AllowLocalFallback=false,
//     we error out (so you remember to add the tool to your MCP server).
//
// Replace planner kickoff with your API if needed.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ==========================================================
// App configuration & constants
// ==========================================================

// Capability identifiers used throughout the orchestrator.
const (
	CapWebSearch   = "web.search"
	CapWebFetch    = "web.fetch"
	CapWebIngest   = "web.ingest"
	CapSearchQuery = "search.query"
	CapEmbedding   = "embedding"
)

// CapabilityTools maps high-level capabilities to ordered tool candidates.
// You can change these names without touching worker logic.
var CapabilityTools = map[string][]string{
	CapWebSearch:   {"web.search.brave", "web.search.serper"},
	CapWebFetch:    {"web.fetch.chromedp"},
	CapWebIngest:   {"web.ingest"},
	CapSearchQuery: {"search.query"},
	CapEmbedding:   {"embedding.embed_many"},
}

// MCP call defaults/timeouts.
const (
	DefaultToolTimeout = 35 * time.Second
	MaxJSONFrameBytes  = 1 << 20 // 1MB safety cap for a single JSON-RPC frame
)

// AppConfig centralizes knobs for behavior and fallbacks.
// Populate from env vars in main(); tweak as you see fit.
type AppConfig struct {
	// MCP agent commands. You can point multiple roles at the same binary.
	SearchCmd string
	FetchCmd  string
	IngestCmd string // optional, if you prefer a dedicated ingest agent

	// If true, the worker is allowed to use local fallbacks when an MCP capability
	// is missing. Recommended FALSE so you remember to add the tool to your MCP server.
	AllowLocalFallback bool

	// Per-tool call timeouts.
	ToolTimeout time.Duration
}

// loadConfigFromEnv builds a simple AppConfig from environment variables.
func loadConfigFromEnv() AppConfig {
	cfg := AppConfig{
		SearchCmd:          strings.TrimSpace(os.Getenv("MCP_SEARCH_CMD")),
		FetchCmd:           strings.TrimSpace(os.Getenv("MCP_FETCH_CMD")),
		IngestCmd:          strings.TrimSpace(os.Getenv("MCP_INGEST_CMD")),
		AllowLocalFallback: strings.EqualFold(os.Getenv("ALLOW_LOCAL_FALLBACK"), "true"),
		ToolTimeout:        DefaultToolTimeout,
	}
	if v := strings.TrimSpace(os.Getenv("MCP_TOOL_TIMEOUT_SEC")); v != "" {
		if n, err := time.ParseDuration(v + "s"); err == nil {
			cfg.ToolTimeout = n
		}
	}
	return cfg
}

// ==========================================================
// Domain types
// ==========================================================

type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

type Stage string

const (
	StageKickoff  Stage = "kickoff"
	StageDiscover Stage = "discover"
	StageFetch    Stage = "fetch"
	StageIngest   Stage = "ingest"
	StageComplete Stage = "complete"
	StageError    Stage = "error"
)

// Goal is the high-level user intent the planner decomposes into tasks.
// Queries are sent to search; results are fetched; content is ingested via MCP.
type Goal struct {
	Label       string
	Queries     []string
	Sites       []string
	MaxResults  int
	RecencyDays int
	SessionID   string // optional session identifier for web.ingest
	TTLHours    int    // TTL for ingested docs
	Priority    Priority
}

// StreamItem is an observable log/event we can persist and later summarize.
type StreamItem struct {
	RunID string    `json:"run_id"`
	Stage Stage     `json:"stage"`
	Title string    `json:"title,omitempty"`
	URL   string    `json:"url,omitempty"`
	Note  string    `json:"note,omitempty"`
	Err   string    `json:"err,omitempty"`
	Time  time.Time `json:"time"`
}

// ==========================================================
// Run tracking & counting queue
// ==========================================================

// RunTracker counts outstanding tasks per run and triggers completion when zero.
type RunTracker struct {
	mu      sync.Mutex
	pending map[string]int
	onZero  func(runID string)
}

// NewRunTracker constructs a RunTracker.
func NewRunTracker() *RunTracker {
	return &RunTracker{pending: map[string]int{}}
}

// SetOnZero registers a callback invoked when a run reaches zero pending tasks.
func (rt *RunTracker) SetOnZero(fn func(runID string)) { rt.onZero = fn }

// Inc increments the pending count for a run.
func (rt *RunTracker) Inc(runID string) { rt.mu.Lock(); rt.pending[runID]++; rt.mu.Unlock() }

// Dec decrements the pending count for a run and fires onZero at zero.
func (rt *RunTracker) Dec(runID string) {
	var hitZero bool
	rt.mu.Lock()
	if n := rt.pending[runID]; n > 0 {
		rt.pending[runID] = n - 1
		if rt.pending[runID] == 0 {
			hitZero = true
		}
	}
	rt.mu.Unlock()
	if hitZero && rt.onZero != nil {
		rt.onZero(runID)
	}
}

// Queue is a minimal interface for task dispatch.
type Queue interface {
	Enqueue(ctx context.Context, t Task) error
	Dequeue(ctx context.Context) (Task, error)
}

// CountingQueue wraps an inner Queue and updates the tracker on enqueue.
type CountingQueue struct {
	inner   Queue
	tracker *RunTracker
}

// NewCountingQueue constructs a counting queue wrapper.
func NewCountingQueue(inner Queue, tracker *RunTracker) *CountingQueue {
	return &CountingQueue{inner: inner, tracker: tracker}
}

// Enqueue adds a task and increments the pending run counter.
func (q *CountingQueue) Enqueue(ctx context.Context, t Task) error {
	if q.tracker != nil {
		q.tracker.Inc(t.RunID)
	}
	return q.inner.Enqueue(ctx, t)
}

// Dequeue returns the next available task.
func (q *CountingQueue) Dequeue(ctx context.Context) (Task, error) { return q.inner.Dequeue(ctx) }

// ==========================================================
// Result persistence (simple in-memory + file JSON summary)
// ==========================================================

type AgentRunRepository interface {
	Save(ctx context.Context, s StreamItem) error
	List(ctx context.Context, runID string) ([]StreamItem, error)
}

type InMemoryRepo struct {
	mu   sync.Mutex
	data map[string][]StreamItem
}

// NewInMemoryRepo constructs a new in-memory repository of events.
func NewInMemoryRepo() *InMemoryRepo { return &InMemoryRepo{data: map[string][]StreamItem{}} }

// Save appends a StreamItem for a run.
func (r *InMemoryRepo) Save(_ context.Context, s StreamItem) error {
	r.mu.Lock()
	r.data[s.RunID] = append(r.data[s.RunID], s)
	r.mu.Unlock()
	return nil
}

// List returns a copy of the run's events.
func (r *InMemoryRepo) List(_ context.Context, runID string) ([]StreamItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]StreamItem, len(r.data[runID]))
	copy(cp, r.data[runID])
	return cp, nil
}

type RunResultSink interface {
	Save(ctx context.Context, runID string, items []StreamItem) error
}

type FileRunSink struct{ dir string }

// NewFileRunSink ensures the directory exists and returns a sink writing JSON files.
func NewFileRunSink(dir string) (*FileRunSink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileRunSink{dir: dir}, nil
}

type RunSummary struct {
	RunID        string       `json:"run_id"`
	CompletedAt  time.Time    `json:"completed_at"`
	DocsIngested int          `json:"docs_ingested"`
	Items        []StreamItem `json:"items"`
}

// Save writes a run summary to ./runs/<id>.json.
func (s *FileRunSink) Save(_ context.Context, runID string, items []StreamItem) error {
	ingests := 0
	for _, it := range items {
		if it.Stage == StageIngest && strings.Contains(it.Note, "indexed+embedded") {
			ingests++
		}
	}
	summary := RunSummary{RunID: runID, CompletedAt: time.Now(), DocsIngested: ingests, Items: items}
	b, _ := json.MarshalIndent(summary, "", "  ")
	path := fmt.Sprintf("%s/%s.json", s.dir, runID)
	return os.WriteFile(path, b, 0o644)
}

// ==========================================================
// Basic in-memory queue
// ==========================================================

type TaskType string

const (
	TaskDiscover TaskType = "discover"
	TaskFetch    TaskType = "fetch"
	TaskIngest   TaskType = "ingest"
)

// Task is a unit of work produced by the planner and consumed by workers.
type Task struct {
	ID       string
	Type     TaskType
	RunID    string
	Payload  map[string]any
	Priority Priority
}

type inmemQueue struct{ ch chan Task }

// NewInMemQueue returns a buffered in-memory queue.
func NewInMemQueue(buf int) Queue { return &inmemQueue{ch: make(chan Task, buf)} }

// Enqueue pushes a task into the queue or returns ctx.Err on cancellation.
func (q *inmemQueue) Enqueue(ctx context.Context, t Task) error {
	select {
	case q.ch <- t:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dequeue blocks until a task is available or ctx is cancelled.
func (q *inmemQueue) Dequeue(ctx context.Context) (Task, error) {
	select {
	case t := <-q.ch:
		return t, nil
	case <-ctx.Done():
		return Task{}, ctx.Err()
	}
}

// ==========================================================
// MCP (Model Context Protocol) minimal JSON-RPC client over stdio
// ==========================================================

// MCPClient is the minimal interface we need for MCP agents.
type MCPClient interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
	Close() error
}

// MCPTool mirrors tool metadata exposed by the MCP server.
type MCPTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]any    `json:"input_schema"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type stdioMCP struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	mu  sync.Mutex
	seq int64
}

// StartStdioMCP spawns an MCP server process and wires stdin/stdout.
func StartStdioMCP(ctx context.Context, command string, args ...string) (MCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr // keep stdout clean for JSON-RPC frames only
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &stdioMCP{cmd: cmd, in: stdin, out: bufio.NewReader(stdout)}, nil
}

// JSON-RPC wire types

type rpcReq struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// send performs a full req/resp roundtrip under a single mutex to avoid
// races on the shared bufio.Reader. It also defends against noisy stdout and
// abnormally large frames.
func (c *stdioMCP) send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := c.seq
	req := rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if _, err := c.in.Write(b); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(DefaultToolTimeout)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("mcp: timeout waiting for %s", method)
		}

		var buf bytes.Buffer
		for {
			frag, err := c.out.ReadBytes('\n')
			buf.Write(frag)
			if buf.Len() > MaxJSONFrameBytes {
				return nil, fmt.Errorf("mcp: frame too large")
			}
			if err == nil {
				break
			}
			if err == io.EOF {
				return nil, io.EOF
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				return nil, err
			}
		}
		line := bytes.TrimSpace(buf.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var resp rpcResp
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// ListTools queries the agent's available tools.
func (c *stdioMCP) ListTools(ctx context.Context) ([]MCPTool, error) {
	res, err := c.send(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	raw, ok := res["tools"].([]any)
	if !ok {
		return nil, errors.New("invalid tools/list result")
	}
	out := make([]MCPTool, 0, len(raw))
	for _, v := range raw {
		b, _ := json.Marshal(v)
		var t MCPTool
		_ = json.Unmarshal(b, &t)
		out = append(out, t)
	}
	return out, nil
}

// CallTool invokes a specific tool with arguments and returns the raw result map.
func (c *stdioMCP) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	return c.send(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
}

// Close waits for the MCP process to exit.
func (c *stdioMCP) Close() error { _ = c.in.Close(); return c.cmd.Wait() }

// ==========================================================
// Agent registry
// ==========================================================

type Agent struct {
	Name   string
	Client MCPClient
	Tools  map[string]MCPTool
	Can    map[string]bool
}

type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

// NewAgentRegistry builds an empty registry.
func NewAgentRegistry() *AgentRegistry { return &AgentRegistry{agents: map[string]*Agent{}} }

// Add registers an agent in the registry.
func (r *AgentRegistry) Add(a *Agent) { r.mu.Lock(); r.agents[a.Name] = a; r.mu.Unlock() }

// All returns a snapshot of all registered agents.
func (r *AgentRegistry) All() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}

// FindAgentFor returns the first agent that advertises the given capability.
func (r *AgentRegistry) FindAgentFor(cap string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.agents {
		if a.Can[cap] {
			return a
		}
	}
	return nil
}

// primeAgent introspects tools for an agent and sets capability flags based on
// CapabilityTools mapping. If a tool name listed in CapabilityTools is present,
// the corresponding capability is marked true.
func primeAgent(ctx context.Context, a *Agent) error {
	tools, err := a.Client.ListTools(ctx)
	if err != nil {
		return err
	}
	a.Tools = map[string]MCPTool{}
	a.Can = map[string]bool{}
	for _, t := range tools {
		a.Tools[t.Name] = t
	}
	for capName, candidates := range CapabilityTools {
		for _, tool := range candidates {
			if _, ok := a.Tools[tool]; ok {
				a.Can[capName] = true
				break
			}
		}
	}
	return nil
}

// ==========================================================
// Planner
// ==========================================================

type Planner struct{}

type PlannedTask struct {
	Kind TaskType
	Args map[string]any
}

type Plan struct{ Steps []PlannedTask }

// MakePlan converts a Goal into an initial Plan containing discover steps only.
// Further fetch/ingest steps are spawned based on tool results at runtime.
func (p Planner) MakePlan(goal Goal) Plan {
	var steps []PlannedTask
	k := clamp(goal.MaxResults, 1, 25)
	for _, q := range goal.Queries {
		steps = append(steps, PlannedTask{Kind: TaskDiscover, Args: map[string]any{
			"query": q, "k": k, "sites": goal.Sites, "recency": goal.RecencyDays,
			"ttl": goal.TTLHours, "session_id": goal.SessionID,
		}})
	}
	return Plan{Steps: steps}
}

// clamp bounds an integer to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ==========================================================
// Master orchestrator
// ==========================================================

type Master struct {
	Queue   Queue
	Agents  *AgentRegistry
	Repo    AgentRunRepository
	Stream  chan<- StreamItem
	Sink    RunResultSink
	Tracker *RunTracker
	Cfg     AppConfig
}

// emit records and optionally persists an event.
func (m *Master) emit(run string, st Stage, title, url, note, errMsg string) {
	item := StreamItem{RunID: run, Stage: st, Title: title, URL: url, Note: note, Err: errMsg, Time: time.Now()}
	if m.Stream != nil {
		select {
		case m.Stream <- item:
		default:
		}
	}
	if m.Repo != nil {
		_ = m.Repo.Save(context.Background(), item)
	}
}

// IntrospectAgents queries each agent for tools and capability flags.
func (m *Master) IntrospectAgents(ctx context.Context) error {
	for _, a := range m.Agents.All() {
		if err := primeAgent(ctx, a); err != nil {
			return fmt.Errorf("%s tools/list: %w", a.Name, err)
		}
		m.emit("introspect", StageKickoff, a.Name, "", fmt.Sprintf("tools=%d", len(a.Tools)), "")
	}
	return nil
}

// onRunComplete emits a final COMPLETE event and writes a JSON summary.
func (m *Master) onRunComplete(runID string) {
	m.emit(runID, StageComplete, "done", "", "all tasks finished", "")
	if m.Repo != nil && m.Sink != nil {
		if items, err := m.Repo.List(context.Background(), runID); err == nil {
			_ = m.Sink.Save(context.Background(), runID, items)
		}
	}
}

// Kickoff creates a run, enqueues discover tasks, and returns the run ID.
func (m *Master) Kickoff(ctx context.Context, g Goal) (string, error) {
	runID := randomID()
	m.emit(runID, StageKickoff, g.Label, "", "planning", "")
	plan := (Planner{}).MakePlan(g)
	for _, step := range plan.Steps {
		t := Task{ID: randomID(), Type: step.Kind, RunID: runID, Priority: g.Priority, Payload: step.Args}
		if err := m.Queue.Enqueue(ctx, t); err != nil {
			return "", err
		}
	}
	return runID, nil
}

// ==========================================================
// Worker
// ==========================================================

type Worker struct {
	ID      string
	Queue   Queue
	Agents  *AgentRegistry
	Master  *Master
	Tracker *RunTracker
}

// Run consumes tasks and processes them until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		t, err := w.Queue.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		func() {
			defer func() {
				if w.Tracker != nil {
					w.Tracker.Dec(t.RunID)
				}
			}()
			if err := w.handle(ctx, t); err != nil {
				w.Master.emit(t.RunID, StageError, string(t.Type), "", "", err.Error())
			}
		}()
	}
}

// handle routes task types to their specific executors.
func (w *Worker) handle(ctx context.Context, t Task) error {
	switch t.Type {
	case TaskDiscover:
		return w.execDiscover(ctx, t)
	case TaskFetch:
		return w.execFetch(ctx, t)
	case TaskIngest:
		return w.execIngest(ctx, t)
	default:
		return nil
	}
}

// execDiscover calls an MCP web.search tool, extracts URLs, and enqueues fetch tasks.
func (w *Worker) execDiscover(ctx context.Context, t Task) error {
	ag := w.Agents.FindAgentFor(CapWebSearch)
	if ag == nil {
		return errors.New("no agent with web.search")
	}
	q := str(t.Payload["query"])
	k := asInt(t.Payload["k"], 10)
	sites := asStrSlice(t.Payload["sites"])
	recency := asInt(t.Payload["recency"], 7)
	// propagate run-level context to successors
	ttl := asInt(t.Payload["ttl"], 48)
	sessionID := str(t.Payload["session_id"])

	tool := pickTool(ag, CapWebSearch)
	ctx2, cancel := context.WithTimeout(ctx, w.Master.Cfg.ToolTimeout)
	defer cancel()
	res, err := ag.Client.CallTool(ctx2, tool, map[string]any{"query": q, "k": k, "sites": sites, "recency": recency})
	if err != nil {
		return err
	}
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}
	urls := extractURLs(res)
	w.Master.emit(t.RunID, StageDiscover, q, "", fmt.Sprintf("%d urls", len(urls)), "")
	for _, u := range urls {
		_ = w.Queue.Enqueue(ctx, Task{ID: randomID(), Type: TaskFetch, RunID: t.RunID, Priority: t.Priority, Payload: map[string]any{
			"url": u, "ttl": ttl, "session_id": sessionID,
		}})
	}
	return nil
}

// execFetch calls an MCP web.fetch tool and enqueues ingest when we have text.
func (w *Worker) execFetch(ctx context.Context, t Task) error {
	ag := w.Agents.FindAgentFor(CapWebFetch)
	if ag == nil {
		return errors.New("no agent with web.fetch")
	}
	u := str(t.Payload["url"])
	if u == "" {
		return errors.New("fetch: empty url")
	}
	tool := pickTool(ag, CapWebFetch)
	ctx2, cancel := context.WithTimeout(ctx, w.Master.Cfg.ToolTimeout)
	defer cancel()
	res, err := ag.Client.CallTool(ctx2, tool, map[string]any{"url": u})
	if err != nil {
		return err
	}
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}

	title := str(res["title"])
	text := str(res["text"])
	pub := str(res["published_at"])
	status := asInt(res["status"], 200)
	w.Master.emit(t.RunID, StageFetch, title, u, fmt.Sprintf("status=%d chars=%d", status, len(text)), "")
	if strings.TrimSpace(text) == "" { // nothing to ingest
		w.Master.emit(t.RunID, StageIngest, title, u, "skip empty", "")
		return nil
	}
	return w.Queue.Enqueue(ctx, Task{ID: randomID(), Type: TaskIngest, RunID: t.RunID, Priority: PriorityHigh, Payload: map[string]any{
		"url": u, "title": title, "text": text, "published_at": pub,
		"ttl": asInt(t.Payload["ttl"], 48), "session_id": str(t.Payload["session_id"]),
	}})
}

// execIngest calls MCP web.ingest with the document. If the capability is
// missing and AllowLocalFallback=false, it returns an error so you remember to
// add the tool to your MCP server.
func (w *Worker) execIngest(ctx context.Context, t Task) error {
	u := str(t.Payload["url"])
	title := str(t.Payload["title"])
	text := str(t.Payload["text"])
	pub := str(t.Payload["published_at"])
	if strings.TrimSpace(text) == "" {
		w.Master.emit(t.RunID, StageIngest, title, u, "skip empty", "")
		return nil
	}

	ag := w.Agents.FindAgentFor(CapWebIngest)
	if ag == nil {
		if !w.Master.Cfg.AllowLocalFallback {
			return errors.New("no agent with web.ingest; expose this tool on your MCP server or enable AllowLocalFallback")
		}
		// If you truly need a local fallback, implement it here. We intentionally
		// omit local ingestion to keep the pipeline MCP-first.
		return errors.New("local ingest fallback not implemented; set AllowLocalFallback=false and add web.ingest tool")
	}
	tool := pickTool(ag, CapWebIngest)
	ctx2, cancel := context.WithTimeout(ctx, w.Master.Cfg.ToolTimeout)
	defer cancel()
	args := map[string]any{
		"session_id": str(t.Payload["session_id"]),
		"ttl_hours":  asInt(t.Payload["ttl"], 48),
		"docs":       []any{map[string]any{"url": u, "title": title, "text": text, "published_at": pub}},
	}
	if _, err := ag.Client.CallTool(ctx2, tool, args); err != nil {
		return err
	}
	w.Master.emit(t.RunID, StageIngest, title, u, "indexed+embedded", "")
	return nil
}

// ==========================================================
// Utilities
// ==========================================================

// randomID produces a short run/task identifier.
func randomID() string { return fmt.Sprintf("r%08x", rand.Uint32()) }

// str best-effort casts interface{} to string.
func str(v any) string { s, _ := v.(string); return s }

// asInt safely extracts an int with a default.
func asInt(v any, def int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return def
	}
}

// asStrSlice extracts []string from []any.
func asStrSlice(v any) []string {
	if v == nil {
		return nil
	}
	s, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, e := range s {
		if z, ok := e.(string); ok {
			out = append(out, z)
		}
	}
	return out
}

// pickTool selects the first available tool for a capability on a given agent.
func pickTool(a *Agent, capability string) string {
	cands := CapabilityTools[capability]
	for _, name := range cands {
		if _, ok := a.Tools[name]; ok {
			return name
		}
	}
	// Fallback to any tool containing the capability token in its name (lenient)
	for n := range a.Tools {
		if strings.Contains(strings.ToLower(n), strings.ToLower(capability)) {
			return n
		}
	}
	// As last resort, return first tool name to avoid empty string (will likely fail fast)
	for n := range a.Tools {
		return n
	}
	return ""
}

// extractURLs pulls URLs from common MCP tool response shapes.
// It gracefully handles structures like:
//
//	{ urls: ["https://..."] }
//	{ results: [{url:"..."},{link:"..."}, ...] }
//	{ web: { results: [...] } }, { items: [...] }, etc.
//
// It deduplicates, normalizes protocol-relative links, and filters to http/https.
func extractURLs(res map[string]any) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if strings.HasPrefix(u, "//") {
			u = "https:" + u
		}
		if !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}
	if v, ok := res["urls"]; ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					add(s)
					continue
				}
				if m, ok := e.(map[string]any); ok {
					if s, _ := m["url"].(string); s != "" {
						add(s)
					}
					if s, _ := m["link"].(string); s != "" {
						add(s)
					}
					if s, _ := m["href"].(string); s != "" {
						add(s)
					}
				}
			}
		}
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range []string{"url", "link", "href"} {
				if s, ok := x[k].(string); ok {
					add(s)
				}
			}
			for _, k := range []string{"results", "items", "data", "web", "news"} {
				if child, ok := x[k]; ok {
					walk(child)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, k := range []string{"result", "results", "items", "data", "web", "news"} {
		if v, ok := res[k]; ok {
			walk(v)
		}
	}
	if len(out) == 0 {
		walk(res)
	}
	return out
}

// ==========================================================
// Main (bootstrapping)
// ==========================================================

// main boots the orchestrator, spawns MCP agents, and runs a sample plan.
// Replace the kickoff with your own API/CLI as needed.
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := loadConfigFromEnv()
	ctx := context.Background()

	reg := NewAgentRegistry()
	// Start a search-capable agent (can be the same binary as fetch/ingest)
	if cfg.SearchCmd != "" {
		if client, err := StartStdioMCP(ctx, cfg.SearchCmd); err != nil {
			log.Fatalf("start search MCP: %v", err)
		} else {
			ag := &Agent{Name: "search-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
			_ = primeAgent(ctx, ag)
			reg.Add(ag)
		}
	}
	if cfg.FetchCmd != "" {
		if client, err := StartStdioMCP(ctx, cfg.FetchCmd); err != nil {
			log.Fatalf("start fetch MCP: %v", err)
		} else {
			ag := &Agent{Name: "fetch-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
			_ = primeAgent(ctx, ag)
			reg.Add(ag)
		}
	}
	if cfg.IngestCmd != "" {
		if client, err := StartStdioMCP(ctx, cfg.IngestCmd); err != nil {
			log.Fatalf("start ingest MCP: %v", err)
		} else {
			ag := &Agent{Name: "ingest-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
			_ = primeAgent(ctx, ag)
			reg.Add(ag)
		}
	}
	if len(reg.All()) == 0 {
		log.Println("[warn] No MCP agents provided. Set MCP_*_CMD; planner will be idle.")
	}

	repo := NewInMemoryRepo()
	stream := make(chan StreamItem, 1024)
	tracker := NewRunTracker()
	q := NewCountingQueue(NewInMemQueue(2048), tracker)
	sink, err := NewFileRunSink("runs")
	if err != nil {
		log.Fatal(err)
	}

	master := &Master{Queue: q, Agents: reg, Repo: repo, Stream: stream, Sink: sink, Tracker: tracker, Cfg: cfg}
	if err := master.IntrospectAgents(ctx); err != nil {
		log.Printf("introspect: %v", err)
	}
	tracker.SetOnZero(func(runID string) { master.onRunComplete(runID) })

	// Start workers
	workers := 4
	for i := 0; i < workers; i++ {
		w := &Worker{ID: fmt.Sprintf("w%d", i+1), Queue: q, Agents: reg, Master: master, Tracker: tracker}
		go func() { _ = w.Run(ctx) }()
	}

	// Stream printer
	go func() {
		for s := range stream {
			log.Printf("[%s] %-9s url=%s title=%s note=%s err=%s", s.RunID, s.Stage, s.URL, s.Title, s.Note, s.Err)
		}
	}()

	// Sample run (replace with your API/CLI)
	run, err := master.Kickoff(ctx, Goal{Label: "sample-news-run", Queries: []string{"OpenAI o3 news", "EU AI Act status"}, MaxResults: 5, RecencyDays: 14, TTLHours: 72, Priority: PriorityHigh})
	if err != nil {
		log.Fatal(err)
	}
	log.Println("run:", run)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}
