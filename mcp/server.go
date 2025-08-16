// mcp/server.go
// Minimal MCP stdio server exposing stateless tools.
// - All persistence & sessions are handled ONLY here (boundary).
// - Tools remain pure and operate only on explicit inputs.
//
// Start: `go run mcp/server.go`
// The master/agents connect via stdio JSON-RPC: "tools/list" and "tools/call".

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
	"time"

	// --- project imports: adjust if your paths are different ---
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/mcp/tools/embedding"
	srch "github.com/mohammad-safakhou/newser/mcp/tools/search"
	"github.com/mohammad-safakhou/newser/mcp/tools/web_fetch"
	"github.com/mohammad-safakhou/newser/mcp/tools/web_ingest"
	"github.com/mohammad-safakhou/newser/mcp/tools/web_search"
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/session"
)

// ---------- JSON-RPC skeleton ----------

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

func writeResp(w io.Writer, id any, result map[string]interface{}, err error) {
	resp := rpcResp{JSONRPC: "2.0", ID: id}
	if err != nil {
		resp.Error = &rpcError{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(resp)
}

// ---------- Tool registry ----------

// ToolDesc describes a single MCP tool, including input schema.
type ToolDesc struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// MCPServer holds shared deps (the only “state”).
type MCPServer struct {
	// core deps
	Store  session.Store
	Embed  embedding.Embedding
	Search srch.Hybrid

	// external clients
	Brave  websearch.Brave
	Serper websearch.Serper

	// reusable headless browser
	Fetcher *web_fetch.Fetcher

	// configuration
	DefaultTimeout time.Duration
	MaxChars       int

	// cached tool descriptors
	tools []ToolDesc
}

// NewMCPServer wires dependencies once.
func NewMCPServer() (*MCPServer, error) {
	config.LoadConfig("", false)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Session store: your real implementation here
	store := session.NewRedisStore(
		config.AppConfig.Databases.Redis.Host,
		config.AppConfig.Databases.Redis.Port,
		config.AppConfig.Databases.Redis.DB,
	)

	// Provider + embeddings
	pv, err := provider.NewProvider(provider.OpenAI)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	emb := embedding.NewEmbedding(pv)

	// Hybrid search (stateless)
	h := srch.NewHybrid(*emb)

	// Reusable chromedp fetcher (tweak defaults via env if you like)
	fetcher, err := web_fetch.NewFetcher(30*time.Second, 12000, "NewserMCP/1.0 (+mcp)")
	if err != nil {
		return nil, fmt.Errorf("fetcher: %w", err)
	}

	// Web search clients (API keys via env)
	braveKey := os.Getenv("BRAVE_SEARCH_KEY")
	serperKey := os.Getenv("SERPER_API_KEY")

	srv := &MCPServer{
		Store:          store,
		Embed:          *emb,
		Search:         h,
		Brave:          websearch.Brave{APIKey: braveKey},
		Serper:         websearch.Serper{APIKey: serperKey},
		Fetcher:        fetcher,
		DefaultTimeout: 30 * time.Second,
		MaxChars:       12000,
	}
	srv.initTools()
	return srv, nil
}

// initTools defines schemas and descriptions surfaced to MCP clients.
func (srv *MCPServer) initTools() {
	srv.tools = []ToolDesc{
		{
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
		{
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
		{
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
		{
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
				"required": []string{"session_id", "docs"},
			},
		},
		{
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
		{
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
	}
}

// listTools returns the advertised tool list.
func (srv *MCPServer) listTools() map[string]any {
	return map[string]any{"tools": srv.tools}
}

// callTool dispatches to handler functions.
func (srv *MCPServer) callTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "web.search.brave":
		return srv.tWebSearchBrave(ctx, args)
	case "web.search.serper":
		return srv.tWebSearchSerper(ctx, args)
	case "web.fetch.chromedp":
		return srv.tWebFetchChromedp(ctx, args)
	case "web.ingest":
		return srv.tWebIngest(ctx, args)
	case "search.query":
		return srv.tSearchQuery(ctx, args)
	case "embedding.embed_many":
		return srv.tEmbedMany(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// ---------- Tool handlers ----------

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
	// Normalize to a uniform shape
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
	url := str(args["url"])
	if url == "" {
		return nil, errors.New("url is required")
	}
	timeout := time.Duration(asInt(args["timeout_ms"])) * time.Millisecond
	maxChars := asInt(args["max_chars"])
	// temporarily override fetcher's MaxChars if provided
	origMax := srv.Fetcher.MaxChars
	if maxChars > 0 {
		srv.Fetcher.MaxChars = maxChars
	}
	defer func() { srv.Fetcher.MaxChars = origMax }()

	res, err := srv.Fetcher.Exec(ctx, url, timeout)
	if err != nil {
		return nil, err
	}
	// models.Result → map
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
// Input: session_id (string), docs ([]DocInput), approx_chunk (int, opt), overlap (int, opt).
func (srv *MCPServer) tWebIngest(ctx context.Context, args map[string]any) (map[string]any, error) {
	sid := str(args["session_id"])
	if sid == "" {
		return nil, errors.New("session_id is required")
	}
	// Resolve (or create) the corpus at the boundary; tools remain pure.
	ttl := 48 * time.Hour
	corp, err := srv.Store.EnsureSession(sid, ttl)
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
		docs = append(docs, web_ingest.DocInput{
			URL:         str(m["url"]),
			Title:       str(m["title"]),
			Text:        str(m["text"]),
			PublishedAt: str(m["published_at"]),
		})
	}

	approx := asInt(args["approx_chunk"])
	overlap := asInt(args["overlap"])
	ing := web_ingest.NewIngestor(srv.Embed, approx, overlap)

	resp, err := ing.IngestDocs(ctx, corp, docs)
	if err != nil {
		return nil, err
	}
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

	// boundary-only session lookup
	corp, err := srv.Store.GetSession(sid)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
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

// ---------- helpers ----------

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

// ---------- stdio loop ----------

// Serve runs a simple stdio JSON-RPC loop for MCP.
func (srv *MCPServer) Serve(in io.Reader, out io.Writer) error {
	rd := bufio.NewReader(in)
	dec := json.NewDecoder(rd)
	for {
		var req rpcReq
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// try to skip bad lines
			continue
		}

		switch req.Method {
		case "tools/list":
			writeResp(out, req.ID, map[string]any{"tools": srv.tools}, nil)

		case "tools/call":
			name := ""
			args := map[string]any{}
			if v, ok := req.Params["name"].(string); ok {
				name = v
			}
			if m, ok := req.Params["arguments"].(map[string]any); ok {
				args = m
			}
			// Per-call timeout to avoid stuck handlers
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			res, err := srv.callTool(ctx, name, args)
			cancel()
			writeResp(out, req.ID, res, err)

		default:
			writeResp(out, req.ID, nil, fmt.Errorf("unknown method: %s", req.Method))
		}
	}
}

func main() {
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
