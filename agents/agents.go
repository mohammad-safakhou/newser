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
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mohammad-safakhou/newser/config"
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
	DefaultToolTimeout = 120 * time.Second
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

	mu        sync.Mutex
	runDone   map[string]chan struct{} // runID -> done channel
	runSessID map[string]string
}

func (m *Master) getRunSession(runID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runSessID == nil {
		return ""
	}
	return m.runSessID[runID]
}
func (m *Master) setRunSession(runID, sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	if m.runSessID == nil {
		m.runSessID = map[string]string{}
	}
	m.runSessID[runID] = sessionID
	m.mu.Unlock()
}

// registerRun allocates a completion channel for a run.
func (m *Master) registerRun(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runDone == nil {
		m.runDone = map[string]chan struct{}{}
	}
	if _, ok := m.runDone[runID]; !ok {
		m.runDone[runID] = make(chan struct{})
	}
}

// markRunComplete closes the run's done channel.
func (m *Master) markRunComplete(runID string) {
	m.mu.Lock()
	ch, ok := m.runDone[runID]
	m.mu.Unlock()
	if ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

// WaitRunComplete blocks until Master emits completion for runID (or ctx cancels).
func (m *Master) WaitRunComplete(ctx context.Context, runID string) error {
	m.mu.Lock()
	ch := m.runDone[runID]
	m.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("unknown run: %s", runID)
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	// NEW: generate report (best-effort; don’t fail the run if LLM is unavailable)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = m.GenerateFinalReport(ctx, runID, ReportGenConfig{
			Model:       envOr("REPORT_MODEL", "gpt-4o-mini"),
			APIKey:      strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
			MaxSnippets: 6,
		})
	}()

	m.markRunComplete(runID)
}

type ReportGenConfig struct {
	Model       string
	APIKey      string
	MaxSnippets int
}

func (m *Master) GenerateFinalReport(ctx context.Context, runID string, cfg ReportGenConfig) (string, error) {
	// 1) Load events for the run
	items, err := m.Repo.List(ctx, runID)
	if err != nil {
		return "", err
	}

	// 2) Pull discover query (first discover title) and the ingested sources
	var discoverQuery string
	type Src struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Note  string `json:"note"`
	}
	var sources []Src
	for _, it := range items {
		if it.Stage == StageDiscover && discoverQuery == "" {
			discoverQuery = strings.TrimSpace(it.Title)
		}
		if it.Stage == StageIngest && strings.Contains(it.Note, "indexed+embedded") {
			sources = append(sources, Src{Title: it.Title, URL: it.URL, Note: it.Note})
		}
	}
	if len(sources) == 0 {
		// No ingests → still produce a minimal report
		sources = []Src{}
	}

	// 3) (Optional) Pull a few top snippets from the corpus via MCP search.query
	//    Use the run’s session_id if we have it.
	var snippets []map[string]string
	if cfg.MaxSnippets <= 0 {
		cfg.MaxSnippets = 6
	}
	sessID := m.getRunSession(runID) // your cache populated during ingest
	if sessID != "" && discoverQuery != "" {
		if ag := m.Agents.FindAgentFor(CapSearchQuery); ag != nil {
			tool := pickTool(ag, CapSearchQuery)
			cctx, cancel := context.WithTimeout(ctx, m.Cfg.ToolTimeout)
			defer cancel()
			res, err := ag.Client.CallTool(cctx, tool, map[string]any{
				"session_id": sessID,
				"q":          discoverQuery,
				"k":          cfg.MaxSnippets,
			})
			if err == nil {
				// normalize
				if c, ok := res["content"].(map[string]any); ok {
					res = c
				}
				if arr, ok := res["hits"].([]any); ok {
					for _, v := range arr {
						if m, ok := v.(map[string]any); ok {
							snippets = append(snippets, map[string]string{
								"title":   str(m["title"]),
								"url":     str(m["url"]),
								"snippet": str(m["snippet"]),
							})
						}
					}
				}
			}
		}
	}

	// 4) Build an LLM prompt (or fallback to a deterministic summary)
	type ReportInput struct {
		RunID    string              `json:"run_id"`
		Query    string              `json:"query"`
		Sources  []Src               `json:"sources"`
		Snippets []map[string]string `json:"snippets"`
		When     string              `json:"when"`
	}
	rin := ReportInput{
		RunID:    runID,
		Query:    discoverQuery,
		Sources:  sources,
		Snippets: snippets,
		When:     time.Now().Format(time.RFC3339),
	}

	var markdown string
	if cfg.APIKey != "" && cfg.Model != "" {
		// Call OpenAI for synthesis (JSON → Markdown)
		body := map[string]any{
			"model":       cfg.Model,
			"temperature": 0.3,
			"messages": []map[string]any{
				{"role": "system", "content": "You write concise, factual news briefs. Cite sources inline with [n] and include a final bullet list of sources with titles and links. No preambles."},
				{"role": "user", "content": fmt.Sprintf("INPUT:\n%s\n\nWrite a brief report:\n- Title\n- 4–8 bullet key findings (factual, deduped)\n- A 1-paragraph summary\n- A timeline (if dates present)\n- Sources (numbered) with title + URL", mustJSON(rin))},
			},
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			var raw struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&raw)
			_ = resp.Body.Close()
			if len(raw.Choices) > 0 {
				markdown = strings.TrimSpace(raw.Choices[0].Message.Content)
			}
		} else if err == nil {
			io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
	}

	// Fallback if LLM failed
	if strings.TrimSpace(markdown) == "" {
		var b strings.Builder
		b.WriteString("# Report: " + runID + "\n\n")
		if discoverQuery != "" {
			b.WriteString("**Query:** " + discoverQuery + "\n\n")
		}
		if len(sources) > 0 {
			b.WriteString("## Sources\n")
			for i, s := range sources {
				fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, s.Title, s.URL)
			}
			b.WriteString("\n")
		}
		if len(snippets) > 0 {
			b.WriteString("## Snippets\n")
			for _, sn := range snippets {
				fmt.Fprintf(&b, "- **%s** — %s\n  %s\n\n", sn["title"], sn["url"], sn["snippet"])
			}
		} else {
			b.WriteString("_No snippets available._\n")
		}
		markdown = b.String()
	}

	// 5) Write files
	pathMD := fmt.Sprintf("runs/%s_report.md", runID)
	if err := os.WriteFile(pathMD, []byte(markdown), 0o644); err != nil {
		return "", err
	}
	// Sidecar JSON (inputs for traceability)
	side := map[string]any{
		"run_id":       runID,
		"query":        discoverQuery,
		"sources":      sources,
		"snippets":     snippets,
		"generated_at": time.Now().Format(time.RFC3339),
	}
	bside, _ := json.MarshalIndent(side, "", "  ")
	_ = os.WriteFile(fmt.Sprintf("runs/%s_report.json", runID), bside, 0o644)

	m.emit(runID, StageComplete, "report", "", "written: "+pathMD, "")
	return pathMD, nil
}

// Kickoff creates a run, enqueues discover tasks, and returns the run ID.
func (m *Master) Kickoff(ctx context.Context, g Goal) (string, error) {
	runID := randomID()
	m.registerRun(runID) // NEW
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

// ThinkAndAct runs iterative planning with an LLMPlanner.
// On each iteration it asks the LLM for actions, executes them via MCP,
// waits for completion, summarizes observations, and repeats.
func (m *Master) ThinkAndAct(ctx context.Context, planner LLMPlanner, goal Goal, maxIters int) (string, error) {
	// Snapshot available tools as seen by the registry
	var tools []MCPToolSummary
	for _, ag := range m.Agents.All() {
		for name, t := range ag.Tools {
			tools = append(tools, MCPToolSummary{
				Name: name, Description: t.Description, InputSchema: t.InputSchema,
				Capability: mapToolToCapability(name),
			})
		}
	}

	var finalRun string
	var observations []ObservationSummary

	for iter := 1; iter <= maxIters; iter++ {
		in := PlannerInput{
			Goal: goal, Tools: tools, Observations: observations,
			MaxResults: clamp(goal.MaxResults, 1, 25),
		}
		t1 := time.Now()
		ctxPlan, cancelPlan := context.WithTimeout(ctx, m.Cfg.ToolTimeout)
		out, err := planner.Plan(ctxPlan, in)
		t2 := time.Now()
		fmt.Printf("Planner iteration (Time elapsed %fs) %d/%d: %s\n", t2.Sub(t1).Seconds(), iter, maxIters, mustJSON(out))
		cancelPlan()
		if err != nil {
			return "", fmt.Errorf("planner error: %w", err)
		}
		if out.Stop {
			m.emit("planner", StageComplete, "planner", "", "stop: "+out.WhyStop, "")
			break
		}

		// If the planner suggests new queries, launch a run using your normal pipeline.
		if len(out.NewQueries) > 0 {
			g := goal
			g.Queries = out.NewQueries
			runID, err := m.Kickoff(ctx, g)
			if err != nil {
				return "", err
			}
			finalRun = runID
			// Wait for that run to finish
			if err := m.WaitRunComplete(ctx, runID); err != nil {
				return "", err
			}
			// Summarize observations from the repo
			items, _ := m.Repo.List(ctx, runID)
			observations = summarize(items)
			continue
		}

		// Or the planner can propose explicit tool actions (rarely needed; included for flexibility).
		if len(out.Actions) > 0 {
			// Translate actions → Goal → run (we reuse your discover/fetch/ingest pipeline)
			var queries []string
			for _, a := range out.Actions {
				if a.Capability == CapWebSearch && a.Args != nil {
					if q := str(a.Args["query"]); q != "" {
						queries = append(queries, q)
					}
				}
			}
			if len(queries) == 0 && len(out.NewQueries) == 0 {
				// Nothing actionable; stop.
				m.emit("planner", StageComplete, "planner", "", "no actions", "")
				break
			}
			g := goal
			if len(queries) > 0 {
				g.Queries = queries
			}
			runID, err := m.Kickoff(ctx, g)
			if err != nil {
				return "", err
			}
			finalRun = runID
			if err := m.WaitRunComplete(ctx, runID); err != nil {
				return "", err
			}
			items, _ := m.Repo.List(ctx, runID)
			observations = summarize(items)
			continue
		}

		fmt.Printf("planner iteration %d/%d: %s\n", iter, maxIters, mustJSON(out))
		// If neither queries nor actions are present, we consider done.
		break
	}
	return finalRun, nil
}

func mapToolToCapability(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasPrefix(l, "web.search"):
		return CapWebSearch
	case strings.HasPrefix(l, "web.fetch"):
		return CapWebFetch
	case strings.HasPrefix(l, "web.ingest"):
		return CapWebIngest
	case strings.HasPrefix(l, "search.query"):
		return CapSearchQuery
	case strings.HasPrefix(l, "embedding."):
		return CapEmbedding
	default:
		return ""
	}
}

func summarize(items []StreamItem) []ObservationSummary {
	out := make([]ObservationSummary, 0, len(items))
	for _, it := range items {
		out = append(out, ObservationSummary{
			Stage: it.Stage,
			Title: it.Title,
			URL:   it.URL,
			Note:  it.Note,
			Err:   it.Err,
		})
	}
	return out
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
			return errors.New("no agent with web.ingest; expose this tool or enable AllowLocalFallback")
		}
		return errors.New("local ingest fallback not implemented")
	}

	tool := pickTool(ag, CapWebIngest)
	ctx2, cancel := context.WithTimeout(ctx, w.Master.Cfg.ToolTimeout)
	defer cancel()

	// If payload has no session, try run-scoped cache first
	sid := str(t.Payload["session_id"])
	if sid == "" {
		if cached := w.Master.getRunSession(t.RunID); cached != "" {
			sid = cached
		}
	}

	args := map[string]any{
		"session_id": sid, // may be empty – server will create
		"ttl_hours":  asInt(t.Payload["ttl"], 48),
		"docs": []any{map[string]any{
			"url": u, "title": title, "text": text, "published_at": pub,
		}},
	}

	res, err := ag.Client.CallTool(ctx2, tool, args)
	if err != nil {
		return err
	}
	// capture the session id returned by MCP, cache it for this run
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}
	if sid2, _ := res["session_id"].(string); sid2 != "" && sid2 != sid {
		w.Master.setRunSession(t.RunID, sid2)
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
	config.LoadConfig("", false)

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

	// ---- Autonomous think/act mode (optional) ----
	if strings.EqualFold(os.Getenv("MASTER_AUTONOMOUS"), "true") {
		planner := OpenAIPlanner{
			APIKey: os.Getenv("OPENAI_API_KEY"),
			Model:  envOr("PLANNER_MODEL", "gpt-5-2025-08-07"),
			Temp:   parseFloatEnv("PLANNER_TEMP", 1),
		}
		goal := Goal{
			Label:       envOr("GOAL_LABEL", "sample-news-run"),
			Queries:     []string{"OpenAI o3 news"}, // the planner will propose queries
			Sites:       nil,
			MaxResults:  parseIntEnv("GOAL_MAX_RESULTS", 5),
			RecencyDays: parseIntEnv("GOAL_RECENCY_DAYS", 14),
			SessionID:   envOr("GOAL_SESSION_ID", ""), // pass if you want continuity
			TTLHours:    parseIntEnv("GOAL_TTL_HOURS", 72),
			Priority:    PriorityHigh,
		}
		finalRun, err := master.ThinkAndAct(ctx, planner, goal, parseIntEnv("MASTER_MAX_ITERS", 3))
		if err != nil {
			log.Fatal(err)
		}
		log.Println("final run:", finalRun)
		if finalRun != "" {
			log.Printf("final run: %s", finalRun)
			// Best-effort: point to report file
			p := fmt.Sprintf("runs/%s_report.md", finalRun)
			if _, err := os.Stat(p); err == nil {
				log.Printf("report: %s", p)
			}
		}
	} else {
		// ---- Single-run mode (existing behavior) ----
		run, err := master.Kickoff(ctx, Goal{
			Label:       "sample-news-run",
			Queries:     []string{"OpenAI o3 news", "EU AI Act status"},
			MaxResults:  5,
			RecencyDays: 14,
			TTLHours:    72,
			Priority:    PriorityHigh,
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Println("run:", run)

		// NEW: block until the run is truly done (or time out / ctrl-c)
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := master.WaitRunComplete(waitCtx, run); err != nil {
			log.Printf("wait run error: %v", err)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}

func envOr(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v != "" {
		return v
	}
	return def
}
func parseFloatEnv(k string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func parseIntEnv(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if f, err := strconv.ParseInt(v, 10, 64); err == nil {
			return int(f)
		}
	}
	return def
}

// ===================== LLM: OpenAI Chat Completions (JSON only) =====================

type OpenAIPlanner struct {
	APIKey string
	Model  string  // e.g., "gpt-4o-mini" or anything that does JSON well
	Temp   float64 // 0.2–0.7 recommended
}

// Plan prompts the model to return strict JSON (no chain-of-thought).
func (p OpenAIPlanner) Plan(ctx context.Context, in PlannerInput) (PlannerOutput, error) {
	type message struct {
		Role, Content string `json:"role","content"`
	}
	sys := `You are a planning controller. 
- You control web tools ONLY via JSON actions I can execute.
- Output STRICT JSON matching PlannerOutput. 
- Do NOT include explanations outside the JSON.
- Prefer minimal, focused actions. 
- Stop=true when the goal is satisfied or no more useful actions exist.`

	// We give the tools list & last observations so it can choose the next actions.
	payload := map[string]any{
		"model":           p.Model,
		"temperature":     p.Temp,
		"response_format": map[string]any{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": sys},
			{"role": "user", "content": fmt.Sprintf("INPUT:\n%s", mustJSON(in))},
			{"role": "user", "content": `Return JSON:
{"stop":bool,"why_stop":string,"new_queries":[string], "actions":[{"capability":string,"tool":string,"args":object,"reason":string}]}`},
		},
	}

	reqBody, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return PlannerOutput{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return PlannerOutput{}, fmt.Errorf("openai: status %d: %s", resp.StatusCode, string(b))
	}
	var raw struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return PlannerOutput{}, err
	}

	if len(raw.Choices) == 0 {
		return PlannerOutput{}, errors.New("openai: no choices")
	}
	var out PlannerOutput
	if err := json.Unmarshal([]byte(raw.Choices[0].Message.Content), &out); err != nil {
		return PlannerOutput{}, fmt.Errorf("bad JSON from model: %w; content=%s", err, raw.Choices[0].Message.Content)
	}
	return out, nil
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// ==========================================================
// LLM planner (think → act → observe)
// ==========================================================

// PlannerInput is what we feed the LLM about current goals & observations.
type PlannerInput struct {
	Goal         Goal                 `json:"goal"`
	Tools        []MCPToolSummary     `json:"tools"`
	Observations []ObservationSummary `json:"observations"`
	MaxResults   int                  `json:"max_results"`
}

// MCPToolSummary keeps only what the planner needs to decide actions.
type MCPToolSummary struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Capability  string         `json:"capability"`
}

// ObservationSummary is a compact digest of what happened last iteration.
type ObservationSummary struct {
	Stage Stage  `json:"stage"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Note  string `json:"note,omitempty"`
	Err   string `json:"err,omitempty"`
}

// PlannerAction is the LLM’s proposed step (always executed via MCP).
type PlannerAction struct {
	// High-level capability to hit (e.g., "web.search", "web.fetch", "web.ingest")
	Capability string `json:"capability"`
	// Optional: direct tool name if planner has a preference; else we pick per CapabilityTools mapping.
	Tool string `json:"tool,omitempty"`
	// Arguments passed to MCP tool (must match the tool’s schema)
	Args map[string]any `json:"args"`
	// Optional: short reason (logged only; we won’t expose chain-of-thought)
	Reason string `json:"reason,omitempty"`
}

// PlannerOutput is a structured instruction set for the Master.
type PlannerOutput struct {
	Stop       bool            `json:"stop"`
	WhyStop    string          `json:"why_stop,omitempty"`
	NewQueries []string        `json:"new_queries,omitempty"`
	Actions    []PlannerAction `json:"actions,omitempty"`
}

// LLMPlanner decides “what to do next” from inputs & observations.
type LLMPlanner interface {
	Plan(ctx context.Context, in PlannerInput) (PlannerOutput, error)
}
