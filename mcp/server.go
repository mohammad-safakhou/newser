// mcp/server.go
// Robust MCP stdio server exposing stateless tools.
// - All persistence & sessions are handled ONLY here (boundary).
// - Tools remain pure and operate only on explicit inputs.
// - Production-grade JSON-RPC handling, per-tool timeouts, concurrency caps,
//   metrics, and automatic tools/list introspection.
//
// Start: `go run mcp/server.go`
// Agents connect via stdio JSON-RPC: "initialize", "tools/list", "tools/call", "ping", "metrics/get".

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	// --- project imports: keep aligned with your repo layout ---
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/mcp/tools/embedding"
	srch "github.com/mohammad-safakhou/newser/mcp/tools/search"
	"github.com/mohammad-safakhou/newser/mcp/tools/web_fetch"
	"github.com/mohammad-safakhou/newser/mcp/tools/web_ingest"
	websearch "github.com/mohammad-safakhou/newser/mcp/tools/web_search"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/session"
)

const (
	serverName    = "newser-mcp"
	serverVersion = "1.0.0"
)

// ============================= JSON-RPC =============================

// rpcReq / rpcResp implement a minimal JSON-RPC 2.0 envelope.
type rpcReq struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      any                    `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}
type rpcResp struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      any                    `json:"id"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   *rpcError              `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeResult writes a success response to stdout.
func writeResult(w io.Writer, id any, result map[string]interface{}) {
	resp := rpcResp{JSONRPC: "2.0", ID: id, Result: result}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeErr writes a JSON-RPC error with a standard code.
// Common codes: -32600 invalid request, -32601 method not found,
// -32602 invalid params, -32603 internal error, -32000 server error.
func writeErr(w io.Writer, id any, code int, msg string) {
	resp := rpcResp{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ============================= Tool registry =============================

// ToolDesc is advertised to clients via tools/list.
type ToolDesc struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolFunc is the handler signature for a tool call.
type ToolFunc func(ctx context.Context, args map[string]any) (map[string]any, error)

// Tool holds descriptor + runtime policy (timeout, concurrency).
type Tool struct {
	Desc          ToolDesc
	Handler       ToolFunc
	Timeout       time.Duration // per-call timeout (0 => server default)
	MaxConcurrent int           // 0 => unlimited
	sema          chan struct{} // lazily created if MaxConcurrent > 0
}

func (t *Tool) acquire() func() {
	if t.MaxConcurrent <= 0 {
		return func() {}
	}
	if t.sema == nil {
		t.sema = make(chan struct{}, t.MaxConcurrent)
	}
	t.sema <- struct{}{}
	return func() { <-t.sema }
}

// ============================= Metrics =============================

// Metrics keeps per-tool counters and latencies.
type Metrics struct {
	mu   sync.RWMutex
	data map[string]*metric
}
type metric struct {
	Calls   int64     `json:"calls"`
	Errors  int64     `json:"errors"`
	AvgMS   float64   `json:"avg_ms"`
	LastMS  int64     `json:"last_ms"`
	LastErr string    `json:"last_err,omitempty"`
	Updated time.Time `json:"updated"`
}

func NewMetrics() *Metrics { return &Metrics{data: map[string]*metric{}} }

func (m *Metrics) record(name string, dur time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.data[name]
	if !ok {
		x = &metric{}
		m.data[name] = x
	}
	x.Calls++
	ms := dur.Milliseconds()
	x.LastMS = ms
	if x.Calls == 1 {
		x.AvgMS = float64(ms)
	} else {
		// incremental average
		x.AvgMS = x.AvgMS + (float64(ms)-x.AvgMS)/float64(x.Calls)
	}
	if err != nil {
		x.Errors++
		x.LastErr = err.Error()
	} else {
		x.LastErr = ""
	}
	x.Updated = time.Now()
}

func (m *Metrics) snapshot() map[string]*metric {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*metric, len(m.data))
	for k, v := range m.data {
		cp := *v
		out[k] = &cp
	}
	return out
}

// ============================= Server =============================

// MCPServer owns shared dependencies (boundary state) and the tool registry.
type MCPServer struct {
	// Core deps at the boundary (stateless tools use these via args)
	Store  session.Store
	Embed  embedding.Embedding
	Search srch.Hybrid

	// External HTTP clients
	Brave  websearch.Brave
	Serper websearch.Serper

	// Reusable headless fetcher
	Fetcher *web_fetch.Fetcher

	// Global policy
	DefaultTimeout   time.Duration
	DefaultMaxChars  int
	MaxConcurrentAll int // overall cap (0 => unlimited)
	allSema          chan struct{}

	// Registry & metrics
	tools   map[string]*Tool
	ordered []ToolDesc
	metrics *Metrics

	// Logger
	logger *log.Logger
}

// NewMCPServer wires dependencies once from config/env; tools remain stateless.
func NewMCPServer() (*MCPServer, error) {
	// Log only to stderr so stdout stays pure JSON-RPC.
	logger := log.New(os.Stderr, "[mcp] ", log.LstdFlags|log.Lshortfile)

	// Config load
	config.LoadConfig("", false)

	// Boundary session store (Redis-backed recommended for durability)
	store := session.NewRedisStore(
		config.AppConfig.Databases.Redis.Host,
		config.AppConfig.Databases.Redis.Port,
		config.AppConfig.Databases.Redis.Pass,
		config.AppConfig.Databases.Redis.DB,
	)

	// Provider + embeddings
	pv, err := provider.NewProvider(provider.OpenAI)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	emb := embedding.NewEmbedding(pv)

	// Stateless hybrid search
	h := srch.NewHybrid(*emb)

	// Fetcher (chromedp wrapper) with defaults from env
	defTimeout := parseDurationEnv("MCP_DEFAULT_TIMEOUT", 30*time.Second)
	maxChars := parseIntEnv("MCP_MAX_CHARS", 12000)
	fetcher, err := web_fetch.NewFetcher(defTimeout, maxChars, "NewserMCP/1.0 (+mcp)")
	if err != nil {
		return nil, fmt.Errorf("fetcher: %w", err)
	}

	// Web search API keys from env
	braveKey := os.Getenv("BRAVE_SEARCH_KEY")
	serperKey := os.Getenv("SERPER_API_KEY")

	srv := &MCPServer{
		Store:            store,
		Embed:            *emb,
		Search:           h,
		Brave:            websearch.Brave{APIKey: braveKey},
		Serper:           websearch.Serper{APIKey: serperKey},
		Fetcher:          fetcher,
		DefaultTimeout:   defTimeout,
		DefaultMaxChars:  maxChars,
		MaxConcurrentAll: parseIntEnv("MCP_MAX_CONCURRENT", 0),
		tools:            map[string]*Tool{},
		metrics:          NewMetrics(),
		logger:           logger,
	}

	if srv.MaxConcurrentAll > 0 {
		srv.allSema = make(chan struct{}, srv.MaxConcurrentAll)
	}

	srv.registerTools()
	return srv, nil
}

// registerTools defines schemas and handlers in one place.
// Handlers are wrapped with per-tool timeouts and concurrency caps.
func (srv *MCPServer) registerTools() {
	reg := []struct {
		desc   ToolDesc
		fn     ToolFunc
		tout   time.Duration
		concur int
	}{
		{
			desc: ToolDesc{
				Name:        "web.search.brave",
				Description: "Search the web via Brave API.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":   map[string]any{"type": "string"},
						"k":       map[string]any{"type": "integer", "minimum": 1, "maximum": 25},
						"sites":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"recency": map[string]any{"type": "integer", "minimum": 0},
					},
					"required": []string{"query"},
				},
			},
			fn:   srv.tWebSearchBrave,
			tout: 15 * time.Second,
		},
		{
			desc: ToolDesc{
				Name:        "web.search.serper",
				Description: "Search the web via Serper API.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":   map[string]any{"type": "string"},
						"k":       map[string]any{"type": "integer", "minimum": 1, "maximum": 25},
						"sites":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"recency": map[string]any{"type": "integer", "minimum": 0},
					},
					"required": []string{"query"},
				},
			},
			fn:   srv.tWebSearchSerper,
			tout: 15 * time.Second,
		},
		{
			desc: ToolDesc{
				Name:        "web.fetch.chromedp",
				Description: "Fetch and extract readable content via headless Chrome.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url":        map[string]any{"type": "string"},
						"timeout_ms": map[string]any{"type": "integer", "minimum": 1000},
						"max_chars":  map[string]any{"type": "integer", "minimum": 1000},
					},
					"required": []string{"url"},
				},
			},
			fn:     srv.tWebFetchChromedp,
			tout:   60 * time.Second,
			concur: parseIntEnv("MCP_FETCH_CONCURRENCY", 3),
		},
		{
			desc: ToolDesc{
				Name:        "web.ingest",
				Description: "Chunk, index, and embed documents into a session corpus.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": map[string]any{"type": "string"},
						"docs": map[string]any{"type": "array", "items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"url":          map[string]any{"type": "string"},
								"title":        map[string]any{"type": "string"},
								"text":         map[string]any{"type": "string"},
								"published_at": map[string]any{"type": "string"},
							},
							"required": []string{"text"},
						}},
						"approx_chunk": map[string]any{"type": "integer"},
						"overlap":      map[string]any{"type": "integer"},
					},
					"required": []string{"docs"},
				},
			},
			fn:   srv.tWebIngest,
			tout: 60 * time.Second,
		},
		{
			desc: ToolDesc{
				Name:        "search.query",
				Description: "Hybrid search (BM25 + vector) over a session corpus.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": map[string]any{"type": "string"},
						"q":          map[string]any{"type": "string"},
						"k":          map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
					},
					"required": []string{"session_id", "q"},
				},
			},
			fn:   srv.tSearchQuery,
			tout: 20 * time.Second,
		},
		{
			desc: ToolDesc{
				Name:        "embedding.embed_many",
				Description: "Get embedding vectors for an array of texts via provider.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"texts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"texts"},
				},
			},
			fn:   srv.tEmbedMany,
			tout: 30 * time.Second,
		},
	}

	for _, e := range reg {
		tool := &Tool{
			Desc:          e.desc,
			Handler:       e.fn,
			Timeout:       e.tout,
			MaxConcurrent: e.concur,
		}
		srv.tools[tool.Desc.Name] = tool
		srv.ordered = append(srv.ordered, tool.Desc)
	}
}

// ============================= Tool handlers =============================

// tWebSearchBrave executes a Brave query; returns a normalized result list.
// Input: query (string), k (int), sites ([]string, optional), recency (int days, optional).
func (srv *MCPServer) tWebSearchBrave(ctx context.Context, args map[string]any) (map[string]any, error) {
	q := str(args["query"])
	if q == "" {
		return nil, errors.New("query is required")
	}
	k := clampInt(asInt(args["k"]), 1, 25)
	sites := asStrSlice(args["sites"])
	recency := asInt(args["recency"])

	results, err := srv.Brave.Discover(ctx, q, k, sites, recency)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet})
	}
	return map[string]any{"results": out}, nil
}

// tWebSearchSerper executes a Serper query; returns a normalized result list.
func (srv *MCPServer) tWebSearchSerper(ctx context.Context, args map[string]any) (map[string]any, error) {
	q := str(args["query"])
	if q == "" {
		return nil, errors.New("query is required")
	}
	k := clampInt(asInt(args["k"]), 1, 25)
	sites := asStrSlice(args["sites"])
	recency := asInt(args["recency"])

	results, err := srv.Serper.Discover(ctx, q, k, sites, recency)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet})
	}
	return map[string]any{"results": out}, nil
}

// tWebFetchChromedp fetches & extracts readable content using the reusable fetcher.
// Input: url (string), timeout_ms (optional), max_chars (optional).
func (srv *MCPServer) tWebFetchChromedp(ctx context.Context, args map[string]any) (map[string]any, error) {
	u := str(args["url"])
	if u == "" {
		return nil, errors.New("url is required")
	}
	timeout := time.Duration(asInt(args["timeout_ms"])) * time.Millisecond
	maxChars := asInt(args["max_chars"])

	origMax := srv.Fetcher.MaxChars
	if maxChars > 0 {
		srv.Fetcher.MaxChars = maxChars
	}
	defer func() { srv.Fetcher.MaxChars = origMax }()

	res, err := srv.Fetcher.Exec(ctx, u, timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"url":          res.URL,
		"title":        res.Title,
		"byline":       res.Byline,
		"published_at": res.PublishedAt,
		"text":         res.Text,
		"top_image":    res.TopImage,
		"html_hash":    res.HTMLHash,
		"status":       res.Status,
		"render_ms":    res.RenderMS,
	}, nil
}

// tWebIngest resolves a session corpus and ingests docs via the pure ingestor.
// Input: session_id (string, optional), docs ([]DocInput), approx_chunk (int, opt), overlap (int, opt).
func (srv *MCPServer) tWebIngest(ctx context.Context, args map[string]any) (map[string]any, error) {
	sid := str(args["session_id"]) // may be ""
	ttlHours := asInt(args["ttl_hours"])
	if ttlHours <= 0 {
		ttlHours = 48
	}
	ttl := time.Duration(ttlHours) * time.Hour

	// CREATE or EXTEND session here (boundary keeps state)
	corp, err := srv.Store.EnsureSession(sid, ttl) // if sid=="" -> new session
	if err != nil {
		return nil, fmt.Errorf("ensure session: %w", err)
	}

	rawDocs, ok := args["docs"].([]any)
	if !ok || len(rawDocs) == 0 {
		return nil, errors.New("docs is required (non-empty array)")
	}
	docs := make([]web_ingest.DocInput, 0, len(rawDocs))
	for _, v := range rawDocs {
		m, _ := v.(map[string]any)
		text := str(m["text"])
		if strings.TrimSpace(text) == "" {
			continue
		}
		docs = append(docs, web_ingest.DocInput{
			URL:         str(m["url"]),
			Title:       str(m["title"]),
			Text:        text,
			PublishedAt: str(m["published_at"]),
		})
	}
	if len(docs) == 0 {
		return nil, errors.New("no non-empty docs to ingest")
	}

	approx := asInt(args["approx_chunk"])
	overlap := asInt(args["overlap"])
	ing := web_ingest.NewIngestor(srv.Embed, approx, overlap)

	resp, err := ing.IngestDocs(ctx, corp, docs)
	if err != nil {
		return nil, err
	}
	// IMPORTANT: always return the session_id we actually used/created
	return map[string]any{
		"session_id":  resp.SessionID,
		"chunks":      resp.Chunks,
		"indexed_bm":  resp.IndexedBM,
		"indexed_vec": resp.IndexedVec,
	}, nil
}

// tSearchQuery resolves a session corpus and runs hybrid search.
// Input: session_id (string), q (string), k (int).
func (srv *MCPServer) tSearchQuery(ctx context.Context, args map[string]any) (map[string]any, error) {
	sid := str(args["session_id"])
	if sid == "" {
		return nil, errors.New("session_id is required")
	}
	q := str(args["q"])
	if q == "" {
		return nil, errors.New("q is required")
	}
	k := asInt(args["k"])
	if k < 1 || k > 50 {
		k = 10
	}

	corp, err := srv.Store.GetSession(sid)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if corp == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}

	hits, err := srv.Search.Search(ctx, corp, q, k)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"doc_id":  h.DocID,
			"title":   h.Title,
			"url":     h.URL,
			"snippet": h.Snippet,
			"score":   h.Score,
		})
	}
	return map[string]any{"hits": out}, nil
}

// tEmbedMany embeds raw texts via provider (pure).
// Input: texts ([]string)
func (srv *MCPServer) tEmbedMany(ctx context.Context, args map[string]any) (map[string]any, error) {
	texts := asStrSlice(args["texts"])
	if len(texts) == 0 {
		return map[string]any{"vectors": [][]float32{}}, nil
	}
	vecs, err := srv.Embed.EmbedMany(ctx, texts)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vectors": vecs}, nil
}

// ============================= Helpers =============================

func str(v any) string { s, _ := v.(string); return s }
func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return 0
	}
}
func asStrSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func parseDurationEnv(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
func parseIntEnv(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// validateRequired enforces presence of required args declared in schema.
func validateRequired(schema map[string]any, args map[string]any) error {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	for _, r := range list {
		key, _ := r.(string)
		if key == "" {
			continue
		}
		if _, exists := args[key]; !exists {
			return fmt.Errorf("missing required argument: %s", key)
		}
	}
	return nil
}

// wrapResult builds a consistent envelope {content:{...}, meta:{...}}
func wrapResult(tool string, took time.Duration, payload map[string]any) map[string]any {
	return map[string]any{
		"content": payload,
		"meta": map[string]any{
			"tool": tool,
			"ms":   took.Milliseconds(),
		},
	}
}

// ============================= Stdio loop =============================

// Serve runs a robust stdio JSON-RPC loop for MCP with:
// - per-call timeout,
// - panic recovery,
// - per-tool & global concurrency caps,
// - automatic tools/list,
// - initialize & ping handlers.
func (srv *MCPServer) Serve(in io.Reader, out io.Writer) error {
	rd := bufio.NewReader(in)
	dec := json.NewDecoder(rd)

	for {
		var req rpcReq
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Skip malformed frames (keeps the stream alive).
			srv.logger.Printf("decode error: %v", err)
			continue
		}

		switch req.Method {
		case "initialize":
			writeResult(out, req.ID, map[string]any{
				"serverInfo": map[string]any{
					"name":    serverName,
					"version": serverVersion,
				},
				"capabilities": map[string]any{
					"tools/list":  true,
					"tools/call":  true,
					"metrics/get": true,
					"ping":        true,
				},
			})

		case "ping":
			writeResult(out, req.ID, map[string]any{"ok": true, "ts": time.Now().UTC().Format(time.RFC3339)})

		case "metrics/get":
			writeResult(out, req.ID, map[string]any{"metrics": srv.metrics.snapshot()})

		case "tools/list":
			writeResult(out, req.ID, map[string]any{"tools": srv.ordered})

		case "tools/call":
			// Extract name & args
			name, _ := req.Params["name"].(string)
			args, _ := req.Params["arguments"].(map[string]any)
			if name == "" {
				writeErr(out, req.ID, -32602, "missing param: name")
				continue
			}
			tool, ok := srv.tools[name]
			if !ok {
				writeErr(out, req.ID, -32601, "unknown tool: "+name)
				continue
			}
			// Validate required args per schema
			if err := validateRequired(tool.Desc.InputSchema, args); err != nil {
				writeErr(out, req.ID, -32602, err.Error())
				continue
			}

			// Concurrency control (global then per-tool)
			releaseAll := srv.acquireAll()
			releaseTool := tool.acquire()

			start := time.Now()
			res, err := srv.safeCall(tool, args) // includes timeout + panic recovery

			// Release slots
			releaseTool()
			releaseAll()

			// Metrics & response
			srv.metrics.record(name, time.Since(start), err)
			if err != nil {
				srv.logger.Printf("tool error %s: %v", name, err)
				writeErr(out, req.ID, -32000, err.Error())
				continue
			}
			writeResult(out, req.ID, wrapResult(name, time.Since(start), res))

		default:
			writeErr(out, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// acquireAll applies global concurrency cap if configured.
func (srv *MCPServer) acquireAll() func() {
	if srv.allSema == nil {
		return func() {}
	}
	srv.allSema <- struct{}{}
	return func() { <-srv.allSema }
}

// safeCall wraps a tool call with timeout and panic recovery.
func (srv *MCPServer) safeCall(t *Tool, args map[string]any) (res map[string]any, err error) {
	// Per-call timeout: prefer tool.Timeout, else server default
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = srv.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	return t.Handler(ctx, args)
}

// ============================= main =============================

func main() {
	config.LoadConfig("", false)

	srv, err := NewMCPServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init error: %v\n", err)
		os.Exit(1)
	}
	if err := srv.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		os.Exit(1)
	}
}
