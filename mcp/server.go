// tools_mcp_server.go
// A single-file MCP server that exposes your tools/ as MCP tools over stdio (JSON-RPC 2.0).
// Supported tools:
//   - web.search.brave    (tools/web_search/brave)
//   - web.search.serper   (tools/web_search/serper)
//   - web.fetch.chromedp  (tools/web_fetch/chromedp)
//   - web.ingest          (tools/web_ingest + embedding/provider + session store)
//   - search.query        (tools/search)
//   - embedding.embed_many (tools/embedding)
//
// Run:
//   OPENAI_API_KEY=... BRAVE_API_KEY=... SERPER_API_KEY=... go run tools_mcp_server.go
//
// Protocol:
//   - Request: JSON-RPC 2.0, one object per line on stdin
//   - Methods:
//       "tools/list":   -> { tools: [{ name, description, input_schema }] }
//       "tools/call":   -> args { name: string, arguments: object } returns { content: any }
//   - Responses printed one per line to stdout.

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
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	// Project deps
	"github.com/mohammad-safakhou/newser/provider"
	"github.com/mohammad-safakhou/newser/session"
	"github.com/mohammad-safakhou/newser/tools/embedding"
	"github.com/mohammad-safakhou/newser/tools/search"
	fetch "github.com/mohammad-safakhou/newser/tools/web_fetch/chromedp"
	fmodels "github.com/mohammad-safakhou/newser/tools/web_fetch/models"
	wi "github.com/mohammad-safakhou/newser/tools/web_ingest"
	wimodels "github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"github.com/mohammad-safakhou/newser/tools/web_search/brave"
	"github.com/mohammad-safakhou/newser/tools/web_search/serper"
)

// ---------------- JSON-RPC plumbing ----------------

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResp struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func writeResp(w *bufio.Writer, id interface{}, result interface{}, err *rpcError) error {
	resp := rpcResp{JSONRPC: "2.0", ID: id, Result: result, Error: err}
	b, _ := json.Marshal(resp)
	b = append(b, '\n')
	_, e := w.Write(b)
	if e == nil {
		_ = w.Flush()
	}
	return e
}

// ---------------- MCP tool registry ----------------

type ToolDesc struct {
	Name        string                                             `json:"name"`
	Description string                                             `json:"description"`
	InputSchema map[string]any                                     `json:"input_schema"`
	Meta        map[string]string                                  `json:"metadata,omitempty"`
	Call        func(context.Context, map[string]any) (any, error) `json:"-"`
}

type Server struct {
	tools []ToolDesc
	// shared deps
	store     session.Store
	prov      provider.Provider
	emb       *embedding.Embedding
	wi        *wi.Ingest
	brave     brave.Search
	serper    serper.Search
	fetcher   fetch.Fetch
	searchSvc search.Search
}

func (s *Server) listTools() map[string]any {
	arr := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		arr = append(arr, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return map[string]any{"tools": arr}
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	for _, t := range s.tools {
		if t.Name == name {
			return t.Call(ctx, args)
		}
	}
	return nil, fmt.Errorf("unknown tool: %s", name)
}

// ---------------- Tool implementations ----------------

func (s *Server) tWebSearchBrave(ctx context.Context, args map[string]any) (any, error) {
	q := strArg(args, "query")
	k := intArg(args, "k", 10)
	sites := strSliceArg(args, "sites")
	recency := intArg(args, "recency", 7)
	if q == "" {
		return nil, errors.New("query is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.brave.Discover(ctx, q, k, sites, recency)
	if err != nil {
		return nil, err
	}
	// normalize result
	out := make([]map[string]any, 0, len(res))
	for _, r := range res {
		out = append(out, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet})
	}
	return map[string]any{"results": out}, nil
}

func (s *Server) tWebSearchSerper(ctx context.Context, args map[string]any) (any, error) {
	q := strArg(args, "query")
	k := intArg(args, "k", 10)
	sites := strSliceArg(args, "sites")
	recency := intArg(args, "recency", 7)
	if q == "" {
		return nil, errors.New("query is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.serper.Discover(ctx, q, k, sites, recency)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res))
	for _, r := range res {
		out = append(out, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet})
	}
	return map[string]any{"results": out}, nil
}

func (s *Server) tWebFetchChromedp(ctx context.Context, args map[string]any) (any, error) {
	u := strArg(args, "url")
	if u == "" {
		return nil, errors.New("url is required")
	}
	timeoutMS := intArg(args, "timeout_ms", 15000)
	maxChars := intArg(args, "max_chars", 12000)
	loc := fetch.Fetch{TimeoutMS: time.Duration(timeoutMS) * time.Millisecond, MaxChars: maxChars}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond+3*time.Second)
	defer cancel()
	res, err := loc.Exec(ctx, u)
	if err != nil {
		// normalize an error response per your models
		return fmodels.Result{URL: u, Status: 599, RenderMS: int((time.Duration(timeoutMS) * time.Millisecond).Milliseconds())}, nil
	}
	return res, nil
}

func (s *Server) tWebIngest(ctx context.Context, args map[string]any) (any, error) {
	sessID := strArg(args, "session_id")
	ttl := intArg(args, "ttl_hours", 48)
	docsAny, ok := args["docs"].([]any)
	if !ok || len(docsAny) == 0 {
		return nil, errors.New("docs: non-empty array required")
	}
	docs := make([]session.DocInput, 0, len(docsAny))
	for _, e := range docsAny {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		docs = append(docs, session.DocInput{
			URL:         strArg(m, "url"),
			Title:       strArg(m, "title"),
			Text:        strArg(m, "text"),
			PublishedAt: strArg(m, "published_at"),
		})
	}
	if len(docs) == 0 {
		return nil, errors.New("docs: empty after parsing")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	sum, err := s.wi.Ingest(sessID, docs, ttl)
	if err != nil {
		return nil, err
	}
	// convert to public model
	return wimodels.IngestResponse{SessionID: sum.SessionID, Chunks: sum.Chunks, IndexedBM: sum.IndexedBM, IndexedVec: sum.IndexedVec}, nil
}

func (s *Server) tSearchQuery(ctx context.Context, args map[string]any) (any, error) {
	sessionID := strArg(args, "session_id")
	q := strArg(args, "q")
	k := intArg(args, "k", 10)
	if sessionID == "" || q == "" {
		return nil, errors.New("session_id and q are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hits, err := s.searchSvc.Search(sessionID, q, k)
	if err != nil {
		return nil, err
	}
	return map[string]any{"hits": hits}, nil
}

func (s *Server) tEmbedMany(ctx context.Context, args map[string]any) (any, error) {
	arrAny, ok := args["texts"].([]any)
	if !ok || len(arrAny) == 0 {
		return nil, errors.New("texts: non-empty array required")
	}
	texts := make([]string, 0, len(arrAny))
	for _, e := range arrAny {
		if s, ok := e.(string); ok {
			texts = append(texts, s)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	vecs, err := s.emb.EmbedMany(ctx, texts)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vectors": vecs}, nil
}

// ---------------- Helpers ----------------

func strArg(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
func intArg(m map[string]any, k string, def int) int {
	if v, ok := m[k]; ok {
		switch x := v.(type) {
		case float64:
			return int(x)
		case int:
			return x
		case json.Number:
			i, _ := x.Int64()
			return int(i)
		}
	}
	return def
}
func strSliceArg(m map[string]any, k string) []string {
	v, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---------------- Main ----------------

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	// Load configuration
	config.LoadConfig("", false)
	log.SetFlags(0)
	enc := json.NewEncoder(os.Stdout)
	_ = enc // silence lint if unused
	ctx := context.Background()

	// Shared deps
	store := session.NewSessionStore() // swap in your real store
	prov, err := provider.NewProvider(provider.OpenAI)
	if err != nil {
		log.Fatalf("failed to create provider: %v", err)
	}
	emb := embedding.NewEmbedding(prov)
	ing := wi.NewIngest(store, *emb)

	srv := &Server{
		store:     store,
		prov:      prov,
		emb:       emb,
		wi:        ing,
		brave:     brave.Search{ApiKey: os.Getenv("BRAVE_API_KEY")},
		serper:    serper.Search{ApiKey: os.Getenv("SERPER_API_KEY")},
		fetcher:   fetch.Fetch{TimeoutMS: 15 * time.Second, MaxChars: 12000},
		searchSvc: search.NewSearch(store),
	}
	// wire embedding into searchSvc
	srv.searchSvc.Embedding = *emb

	// Register tools
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
			Call: srv.tWebSearchBrave,
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
			Call: srv.tWebSearchSerper,
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
			Call: srv.tWebFetchChromedp,
		},
		{
			Name:        "web.ingest",
			Description: "Chunk, index, and embed documents into a session store.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"ttl_hours":  map[string]any{"type": "integer", "minimum": 1},
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
				},
				"required": []string{"docs"},
			},
			Call: srv.tWebIngest,
		},
		{
			Name:        "search.query",
			Description: "Hybrid search (BM25+vector) over a session's corpus.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"q":          map[string]any{"type": "string"},
					"k":          map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
				},
				"required": []string{"session_id", "q"},
			},
			Call: srv.tSearchQuery,
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
			Call: srv.tEmbedMany,
		},
	}

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)

	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "use of closed") {
				return
			}
			if err == io.EOF {
				return
			}
			log.Printf("read error: %v", err)
			return
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeResp(out, nil, nil, &rpcError{Code: -32700, Message: "parse error", Data: err.Error()})
			continue
		}
		// default id: echo raw id back
		id := any(req.ID)

		switch req.Method {
		case "tools/list":
			_ = writeResp(out, id, srv.listTools(), nil)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				_ = writeResp(out, id, nil, &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()})
				continue
			}
			res, err := srv.callTool(ctx, p.Name, p.Arguments)
			if err != nil {
				_ = writeResp(out, id, nil, &rpcError{Code: -32000, Message: err.Error()})
				continue
			}
			_ = writeResp(out, id, map[string]any{"content": res}, nil)
		default:
			_ = writeResp(out, id, nil, &rpcError{Code: -32601, Message: "method not found"})
		}
	}
}
