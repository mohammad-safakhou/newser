// newser_master_agents_singlefile.go
// Single-file, modular Master–Agents layer with MCP integration for newser
// - Queue is pluggable (in-memory by default)
// - Master inspects available Agents via MCP (Model Context Protocol) and plans a task graph
// - Agents can call MCP tools (e.g., web.search, web.fetch) and local Go utilities (web_ingest)
// - Streams concise, readable updates and optionally persists them through a repository interface
//
// Drop this file into your repo (e.g., cmd/agents/) and `go build`.
// Replace TODO sections to wire your actual session store / provider / redis, etc.

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

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/session"
	"github.com/mohammad-safakhou/newser/tools/embedding"
	webingest "github.com/mohammad-safakhou/newser/tools/web_ingest"
)

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

type Goal struct {
	Label       string
	Queries     []string
	Sites       []string
	MaxResults  int
	RecencyDays int
	SessionID   string // optional: pass to ingest; empty -> EnsureSession
	TTLHours    int
	Priority    Priority
}

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
// Run tracking (counts tasks per run) + counting queue wrapper
// ==========================================================

type RunTracker struct {
	mu      sync.Mutex
	pending map[string]int
	started map[string]time.Time
	onZero  func(runID string)
}

func NewRunTracker() *RunTracker {
	return &RunTracker{
		pending: map[string]int{},
		started: map[string]time.Time{},
	}
}
func (rt *RunTracker) SetOnZero(fn func(runID string)) { rt.onZero = fn }
func (rt *RunTracker) MarkStart(runID string) {
	rt.mu.Lock()
	if _, ok := rt.started[runID]; !ok {
		rt.started[runID] = time.Now()
	}
	rt.mu.Unlock()
}
func (rt *RunTracker) Inc(runID string) {
	rt.mu.Lock()
	rt.pending[runID]++
	rt.mu.Unlock()
}
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

type CountingQueue struct {
	inner   Queue
	tracker *RunTracker
}

func NewCountingQueue(inner Queue, tracker *RunTracker) *CountingQueue {
	return &CountingQueue{inner: inner, tracker: tracker}
}
func (q *CountingQueue) Enqueue(ctx context.Context, t Task) error {
	if q.tracker != nil {
		q.tracker.Inc(t.RunID)
	}
	return q.inner.Enqueue(ctx, t)
}
func (q *CountingQueue) Dequeue(ctx context.Context) (Task, error) {
	return q.inner.Dequeue(ctx)
}

// ==========================================================
// Repository (optional persistence)
// ==========================================================

// ==========================================================
// Result sink (persist a JSON summary per run)
// ==========================================================

type AgentRunRepository interface {
	Save(ctx context.Context, s StreamItem) error
	List(ctx context.Context, runID string) ([]StreamItem, error) // NEW
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

type RunResultSink interface {
	Save(ctx context.Context, runID string, items []StreamItem) error
}

type FileRunSink struct{ dir string }

func NewFileRunSink(dir string) (*FileRunSink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileRunSink{dir: dir}, nil
}

type RunSummary struct {
	RunID        string       `json:"run_id"`
	StartedAt    time.Time    `json:"started_at"`
	CompletedAt  time.Time    `json:"completed_at"`
	Items        []StreamItem `json:"items"`
	DocsIngested int          `json:"docs_ingested"`
}

func (s *FileRunSink) Save(ctx context.Context, runID string, items []StreamItem) error {
	var started, completed time.Time
	ingests := 0
	for _, it := range items {
		if it.Stage == StageKickoff && started.IsZero() {
			started = it.Time
		}
		if it.Stage == StageIngest && strings.Contains(it.Note, "indexed+embedded") {
			ingests++
		}
		if it.Stage == StageComplete {
			completed = it.Time
		}
	}
	if completed.IsZero() {
		completed = time.Now()
	}
	summary := RunSummary{
		RunID:        runID,
		StartedAt:    started,
		CompletedAt:  completed,
		Items:        items,
		DocsIngested: ingests,
	}
	b, _ := json.MarshalIndent(summary, "", "  ")
	path := fmt.Sprintf("%s/%s.json", s.dir, runID)
	return os.WriteFile(path, b, 0o644)
}

// ==========================================================
// Queue (pluggable) – in-memory default
// ==========================================================

type TaskType string

const (
	TaskDiscover TaskType = "discover"
	TaskFetch    TaskType = "fetch"
	TaskIngest   TaskType = "ingest"
)

type Task struct {
	ID       string
	Type     TaskType
	RunID    string
	Payload  map[string]any
	Priority Priority
}

type Queue interface {
	Enqueue(ctx context.Context, t Task) error
	Dequeue(ctx context.Context) (Task, error)
}

type inmemQueue struct{ ch chan Task }

func NewInMemQueue(buf int) Queue { return &inmemQueue{ch: make(chan Task, buf)} }
func (q *inmemQueue) Enqueue(ctx context.Context, t Task) error {
	select {
	case q.ch <- t:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
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

type MCPClient interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
	Close() error
}

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
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &stdioMCP{cmd: cmd, in: stdin, out: bufio.NewReader(stdout)}
	return c, nil
}

// Basic JSON-RPC 2.0 envelope

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

	// Robust read loop: accumulate until newline; skip non-JSON noise; cap frame size
	const maxFrame = 1 << 20 // 1 MB
	for {
		var buf bytes.Buffer
		for {
			frag, err := c.out.ReadBytes('\n')
			buf.Write(frag)
			if buf.Len() > maxFrame {
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
			// continue accumulating on ErrBufferFull
		}
		line := bytes.TrimSpace(buf.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			// drop any noisy stdout lines from child (ensure child logs to stderr)
			continue
		}
		var resp rpcResp
		if err := json.Unmarshal(line, &resp); err != nil {
			// not a full JSON-RPC object; keep scanning
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

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

func (c *stdioMCP) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	res, err := c.send(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	return res, err
}

func (c *stdioMCP) Close() error {
	_ = c.in.Close()
	return c.cmd.Wait()
}

// ==========================================================
// Agent + Registry
// ==========================================================

type Agent struct {
	Name   string
	Client MCPClient
	Tools  map[string]MCPTool // name -> tool
	Can    map[string]bool    // quick lookup labels (web.search, web.fetch)
}

type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

func NewAgentRegistry() *AgentRegistry { return &AgentRegistry{agents: map[string]*Agent{}} }

func (r *AgentRegistry) Add(a *Agent) { r.mu.Lock(); r.agents[a.Name] = a; r.mu.Unlock() }
func (r *AgentRegistry) All() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}

func (r *AgentRegistry) FindTool(label string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.agents {
		if a.Can[label] {
			return a
		}
	}
	return nil
}

// ==========================================================
// Planner (simple rule-based): discover -> fetch -> ingest
// ==========================================================

type Planner struct{}

type PlannedTask struct {
	Kind TaskType
	Args map[string]any
}

type Plan struct{ Steps []PlannedTask }

func (p Planner) MakePlan(goal Goal) Plan {
	var steps []PlannedTask
	for _, q := range goal.Queries {
		steps = append(steps, PlannedTask{Kind: TaskDiscover, Args: map[string]any{
			"query": q, "k": clamp(goal.MaxResults, 1, 25), "sites": goal.Sites, "recency": goal.RecencyDays,
		}})
	}
	return Plan{Steps: steps}
}

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
	Sess    session.Store
	Ingest  *webingest.Ingest
	Sink    RunResultSink // NEW
	Tracker *RunTracker   // NEW
}

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

func (m *Master) IntrospectAgents(ctx context.Context) error {
	for _, a := range m.Agents.All() {
		tools, err := a.Client.ListTools(ctx)
		if err != nil {
			return fmt.Errorf("%s tools/list: %w", a.Name, err)
		}
		a.Tools = map[string]MCPTool{}
		a.Can = map[string]bool{}
		for _, t := range tools {
			a.Tools[t.Name] = t
			lname := strings.ToLower(t.Name)
			// Heuristics: map names to capability labels
			if strings.Contains(lname, "search") {
				a.Can["web.search"] = true
			}
			if strings.Contains(lname, "fetch") || strings.Contains(lname, "scrape") {
				a.Can["web.fetch"] = true
			}
		}
		m.emit("introspect", StageKickoff, a.Name, "", fmt.Sprintf("tools=%d", len(tools)), "")
	}
	return nil
}

func (m *Master) onRunComplete(runID string) {
	// Emit a final COMPLETE event
	m.emit(runID, StageComplete, "done", "", "all tasks finished", "")
	// Persist a JSON summary if configured
	if m.Repo != nil && m.Sink != nil {
		if items, err := m.Repo.List(context.Background(), runID); err == nil {
			_ = m.Sink.Save(context.Background(), runID, items)
		}
	}
}

func (m *Master) Kickoff(ctx context.Context, g Goal) (string, error) {
	runID := randomID()
	if m.Tracker != nil {
		m.Tracker.MarkStart(runID)
	}
	m.emit(runID, StageKickoff, g.Label, "", "planning", "")

	// plan
	plan := (Planner{}).MakePlan(g)
	for _, step := range plan.Steps {
		// discover -> queue task
		t := Task{ID: randomID(), Type: step.Kind, RunID: runID, Priority: g.Priority, Payload: step.Args}
		if err := m.Queue.Enqueue(ctx, t); err != nil {
			return "", err
		}
	}
	return runID, nil
}

// ==========================================================
// Worker – executes tasks by selecting appropriate agent via registry
// ==========================================================

type Worker struct {
	ID      string
	Queue   Queue
	Agents  *AgentRegistry
	Master  *Master
	Tracker *RunTracker // NEW
}

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
		// Always decrement when we finish handling this task
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

func (w *Worker) execDiscover(ctx context.Context, t Task) error {
	ag := w.Agents.FindTool("web.search")
	if ag == nil {
		return errors.New("no agent with web.search")
	}
	q := str(t.Payload["query"])
	k := asInt(t.Payload["k"], 10)
	sites := asStrSlice(t.Payload["sites"])
	recency := asInt(t.Payload["recency"], 7)
	name := "web.search.brave"
	if _, ok := ag.Tools[name]; !ok {
		name = "web.search.serper"
	}
	res, err := ag.Client.CallTool(ctx, name, map[string]any{
		"query": q, "k": k, "sites": sites, "recency": recency,
	})
	if err != nil {
		return err
	}
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}

	urls := extractURLs(res)
	w.Master.emit(t.RunID, StageDiscover, q, "", fmt.Sprintf("%d urls", len(urls)), "")
	for _, u := range urls {
		_ = w.Queue.Enqueue(ctx, Task{ID: randomID(), Type: TaskFetch, RunID: t.RunID, Priority: t.Priority, Payload: map[string]any{"url": u}})
	}
	return nil
}

func (w *Worker) execFetch(ctx context.Context, t Task) error {
	ag := w.Agents.FindTool("web.fetch")
	if ag == nil {
		return errors.New("no agent with web.fetch")
	}
	u := str(t.Payload["url"])
	if u == "" {
		return errors.New("fetch: empty url")
	}
	name := "web.fetch.chromedp"
	if _, ok := ag.Tools[name]; !ok {
		// (optional) fall back to another fetch tool name if you add one later
	}
	res, err := ag.Client.CallTool(ctx, name, map[string]any{"url": u})
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
	return w.Queue.Enqueue(ctx, Task{ID: randomID(), Type: TaskIngest, RunID: t.RunID, Priority: PriorityHigh, Payload: map[string]any{
		"url": u, "title": title, "text": text, "published_at": pub,
	}})
}

func (w *Worker) execIngest(ctx context.Context, t Task) error {
	u := str(t.Payload["url"])
	title := str(t.Payload["title"])
	text := str(t.Payload["text"])
	pub := str(t.Payload["published_at"])
	if strings.TrimSpace(text) == "" {
		w.Master.emit(t.RunID, StageIngest, title, u, "skip empty", "")
		return nil
	}
	// Ensure session & run ingest using your existing web_ingest (which indexes + embeds)
	sessID := "" // let EnsureSession create one
	if m := w.Master; m != nil && m.Ingest != nil {
		_, err := m.Ingest.Ingest(sessID, []session.DocInput{{URL: u, Title: title, Text: text, PublishedAt: pub}}, clamp(asInt(t.Payload["ttl"], 48), 1, 24*7))
		if err != nil {
			return err
		}
	}
	w.Master.emit(t.RunID, StageIngest, title, u, "indexed+embedded", "")
	return nil
}

// ==========================================================
// Utilities
// ==========================================================

func randomID() string { return fmt.Sprintf("r%08x", rand.Uint32()) }

func str(v any) string { s, _ := v.(string); return s }
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

func toolName(a *Agent, hint string) string {
	// prefer exact names; fall back to contains
	for n := range a.Tools {
		if strings.EqualFold(n, hint) {
			return n
		}
	}
	for n := range a.Tools {
		if strings.Contains(strings.ToLower(n), hint) {
			return n
		}
	}
	// default to first tool
	for n := range a.Tools {
		return n
	}
	return hint
}

func primeAgent(ctx context.Context, a *Agent) error {
	tools, err := a.Client.ListTools(ctx)
	if err != nil {
		return err
	}
	a.Tools = map[string]MCPTool{}
	a.Can = map[string]bool{}
	for _, t := range tools {
		a.Tools[t.Name] = t
		lname := strings.ToLower(t.Name)
		if strings.Contains(lname, "search") {
			a.Can["web.search"] = true
		}
		if strings.Contains(lname, "fetch") || strings.Contains(lname, "scrape") {
			a.Can["web.fetch"] = true
		}
	}
	return nil
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

	// unwrap common top-level wrapper
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}

	// direct top-level: urls
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

	// recursive walk through typical containers
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
// Bootstrapping: wire providers/session/ingest + spawn MCP agents
// ==========================================================

func main() {
	// Load configuration
	config.LoadConfig("", false)
	ctx := context.Background()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	// ---- newser wiring (replace with your concrete store/provider) ----
	// Session store
	var store = session.NewSessionStore() // TODO: swap with your real store
	// Provider for embeddings used by web_ingest
	var prov, err = provider.NewProvider(provider.OpenAI)
	if err != nil {
		log.Fatalf("failed to create provider: %v", err)
	}
	emb := embedding.NewEmbedding(prov)
	ing := webingest.NewIngest(store, *emb)

	// ---- Spawn MCP servers (examples) ----
	// Expect an MCP server providing a web search tool (e.g., brave search) and a fetch tool.
	// You can point these to your own MCP servers or wrappers around your existing code.
	reg := NewAgentRegistry()

	// Example: search agent (replace command with your MCP server binary)
	if cmd := os.Getenv("MCP_SEARCH_CMD"); cmd != "" {
		client, err := StartStdioMCP(ctx, cmd)
		if err != nil {
			log.Fatalf("start search MCP: %v", err)
		}
		ag := &Agent{Name: "search-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
		_ = primeAgent(ctx, ag)
		reg.Add(ag)
	}
	// Example: fetch agent
	if cmd := os.Getenv("MCP_FETCH_CMD"); cmd != "" {
		client, err := StartStdioMCP(ctx, cmd)
		if err != nil {
			log.Fatalf("start fetch MCP: %v", err)
		}
		ag := &Agent{Name: "fetch-agent", Client: client, Tools: map[string]MCPTool{}, Can: map[string]bool{}}
		_ = primeAgent(ctx, ag)
		reg.Add(ag)
	}
	if len(reg.All()) == 0 {
		log.Println("[warn] No MCP agents provided. Set MCP_SEARCH_CMD and MCP_FETCH_CMD; planner will be idle.")
	}

	// ---- Orchestrator ----
	repo := NewInMemoryRepo()
	stream := make(chan StreamItem, 1024)

	tracker := NewRunTracker()
	baseQ := NewInMemQueue(2048)
	q := NewCountingQueue(baseQ, tracker)

	sink, err := NewFileRunSink("runs")
	if err != nil {
		log.Fatal(err)
	}

	master := &Master{Queue: q, Agents: reg, Repo: repo, Stream: stream,
		Sess: store, Ingest: ing, Sink: sink, Tracker: tracker}
	if err := master.IntrospectAgents(ctx); err != nil {
		log.Printf("introspect: %v", err)
	}

	// When a run's pending count hits zero, finalize & persist summary
	tracker.SetOnZero(func(runID string) { master.onRunComplete(runID) })

	// Start workers
	workers := 4
	for i := 0; i < workers; i++ {
		w := &Worker{ID: fmt.Sprintf("w%d", i+1), Queue: q, Agents: reg, Master: master, Tracker: tracker}
		go func() { _ = w.Run(ctx) }()
	}

	// Stream printer (replace with SSE/WebSocket)
	go func() {
		for s := range stream {
			log.Printf("[%s] %-9s url=%s title=%s note=%s err=%s", s.RunID, s.Stage, s.URL, s.Title, s.Note, s.Err)
		}
	}()

	// ---- Sample run (replace with your API/CLI) ----
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}
