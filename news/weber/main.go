package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/go-shiori/go-readability"
	"log"
	"math"
	"net/http"
	nurl "net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultTTL        = 48 * time.Hour
	maxCharsDefault   = 20000
	chunkTokensApprox = 1000 // rough char budget per chunk
	chunkOverlapChars = 200
	rrfK              = 60   // reciprocal-rank-fusion constant
	maxEmbDims        = 3072 // upper guard; depends on model
	modelEmbedding    = "text-embedding-3-large"
	modelChat         = "gpt-4o-mini"
	maxParallelFetch  = 4
)

type (
	DocChunk struct {
		DocID        string    `json:"doc_id"`
		URL          string    `json:"url"`
		Title        string    `json:"title"`
		Text         string    `json:"text"`
		PublishedAt  string    `json:"published_at,omitempty"`
		ContentHash  string    `json:"content_hash"`
		Lang         string    `json:"lang,omitempty"`
		IngestedAt   time.Time `json:"ingested_at"`
		ChunkIndex   int       `json:"chunk_index"`
		SourceSessID string    `json:"source_session"`
	}

	embedVec struct {
		DocID string
		Vec   []float32
	}

	Session struct {
		ID        string
		CreatedAt time.Time
		ExpiresAt time.Time
		Bleve     bleve.Index // BM25 over chunks
		Vectors   []embedVec  // in-memory vectors for small corpora
		Meta      map[string]DocChunk
		SeenURLs  map[string]time.Time
		mu        sync.RWMutex
	}

	Store struct {
		sessions map[string]*Session
		mu       sync.RWMutex
	}

	SearchHit struct {
		DocID   string  `json:"doc_id"`
		URL     string  `json:"url"`
		Title   string  `json:"title"`
		Snippet string  `json:"snippet"`
		Score   float64 `json:"score"`
		Rank    int     `json:"rank"`
	}
)

var (
	globalStore  = &Store{sessions: map[string]*Session{}}
	openaiClient *openai.Client
	reSpaces     = regexp.MustCompile(`\s+`)
)

func main() {
	// --- deps ---
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}
	openaiClient = openai.NewClient(apiKey)

	// TTL janitor
	go janitor()

	// HTTP
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	r.Post("/discover", handleDiscover)   // optional (Brave/Serper); you can skip and supply URLs directly
	r.Post("/browser/fetch", handleFetch) // fetch + extract
	r.Post("/ingest", handleIngest)       // create/update ephemeral corpus
	r.Post("/search", handleSearch)       // hybrid search
	r.Get("/seen", handleSeen)            // has this URL been seen in the session?
	r.Post("/summarize", handleSummarize) // LLM with citations

	addr := ":8080"
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

// ========== DISCOVER (optional) ==========

type discoverReq struct {
	Query       string   `json:"query"`
	K           int      `json:"k"`
	SiteLimits  []string `json:"site_limits,omitempty"`
	RecencyDays int      `json:"recency_days,omitempty"`
}

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}
type discoverRes struct {
	Results []Result `json:"results"`
}

func handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	k := req.K
	if k <= 0 || k > 50 {
		k = 10
	}

	// Choose provider based on env
	if os.Getenv("SERPER_API_KEY") != "" {
		res, err := discoverSerper(r.Context(), req.Query, k, req.SiteLimits, req.RecencyDays)
		writeJSON(w, res, err)
		return
	}
	if os.Getenv("BRAVE_API_KEY") != "" {
		res, err := discoverBrave(r.Context(), req.Query, k, req.SiteLimits, req.RecencyDays)
		writeJSON(w, res, err)
		return
	}
	// fallback: tell client to supply URLs
	writeJSON(w, discoverRes{Results: []Result{}}, nil)
}

func discoverSerper(ctx context.Context, q string, k int, sites []string, recency int) (discoverRes, error) {
	// https://serper.dev/ docs
	payload := map[string]any{"q": q, "num": k}
	if len(sites) > 0 {
		payload["site"] = strings.Join(sites, " OR ")
	}
	if recency > 0 {
		payload["tbs"] = fmt.Sprintf("qdr:%d", recency)
	} // quick & dirty

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/search", strings.NewReader(string(body)))
	req.Header.Set("X-API-KEY", os.Getenv("SERPER_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return discoverRes{}, err
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return discoverRes{}, err
	}

	var out discoverRes
	if items, ok := raw["organic"].([]any); ok {
		for i, it := range items {
			if i >= k {
				break
			}
			m := it.(map[string]any)
			out.Results = append(out.Results, Result{
				Title: str(m["title"]), URL: str(m["link"]), Snippet: str(m["snippet"]),
			})
		}
	}
	return out, nil
}

func discoverBrave(ctx context.Context, q string, k int, sites []string, recency int) (discoverRes, error) {
	// https://api.search.brave.com/app/documentation/web-search
	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", urlQuery(q), k)
	if len(sites) > 0 {
		url += "&source=web&freshness=&safesearch=off"
	} // sites filter omitted for brevity
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", os.Getenv("BRAVE_API_KEY"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return discoverRes{}, err
	}
	defer resp.Body.Close()
	var raw struct {
		Web struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return discoverRes{}, err
	}
	var out discoverRes
	for i, r := range raw.Web.Results {
		if i >= k {
			break
		}
		out.Results = append(out.Results, Result{r.Title, r.URL, r.Snippet})
	}
	return out, nil
}

func urlQuery(s string) string { return strings.ReplaceAll(s, " ", "+") }
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// ========== BROWSER/FETCH ==========

type fetchReq struct {
	URL       string `json:"url"`
	TimeoutMS int    `json:"timeout_ms"`
	MaxChars  int    `json:"max_chars"`
}

type fetchRes struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Byline      string `json:"byline,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Text        string `json:"text"`
	TopImage    string `json:"top_image,omitempty"`
	HTMLHash    string `json:"html_hash"`
	Status      int    `json:"status"`
	RenderMS    int    `json:"render_ms"`
}

func mustParseURL(raw string) *nurl.URL {
	u, err := nurl.Parse(raw)
	if err != nil {
		return &nurl.URL{}
	}
	return u
}

func handleFetch(w http.ResponseWriter, r *http.Request) {
	var req fetchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 15000
	}
	if req.MaxChars <= 0 || req.MaxChars > maxCharsDefault {
		req.MaxChars = maxCharsDefault
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()
	t0 := time.Now()

	// Headless browse and get HTML
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.UserAgent("RealTimeAIAgent/1.0 (+contact@example.com)"),
	)
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(actx)
	defer cancelBrowser()

	var html string
	err := chromedp.Run(bctx,
		chromedp.Navigate(req.URL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		writeJSON(w, fetchRes{URL: req.URL, Status: 599, Text: "", RenderMS: int(time.Since(t0) / time.Millisecond)}, nil)
		return
	}

	// Readability extract
	article, err := readability.FromReader(strings.NewReader(html), mustParseURL(req.URL))
	if err != nil {
		writeJSON(w, fetchRes{URL: req.URL, Status: 200, Text: "", RenderMS: int(time.Since(t0) / time.Millisecond)}, nil)
		return
	}
	text := article.TextContent
	if len(text) > req.MaxChars {
		text = text[:req.MaxChars]
	}

	// Hash raw html
	sum := sha1.Sum([]byte(html))
	htmlHash := hex.EncodeToString(sum[:])

	resp := fetchRes{
		URL: req.URL, Title: strings.TrimSpace(article.Title), Byline: strings.TrimSpace(article.Byline),
		PublishedAt: article.SiteName, // often empty; real sites need custom date parsing
		Text:        strings.TrimSpace(reSpaces.ReplaceAllString(text, " ")),
		TopImage:    article.Image, HTMLHash: htmlHash, Status: 200, RenderMS: int(time.Since(t0) / time.Millisecond),
	}
	writeJSON(w, resp, nil)
}

// ========== INGEST (per-session ephemeral corpus) ==========

type ingestReq struct {
	SessionID string     `json:"session_id,omitempty"`
	Docs      []docInput `json:"docs"`
	TTLHours  int        `json:"ttl_hours,omitempty"`
}
type docInput struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	PublishedAt string `json:"published_at,omitempty"`
	Lang        string `json:"lang,omitempty"`
}

type ingestRes struct {
	SessionID  string `json:"session_id"`
	Chunks     int    `json:"chunks"`
	IndexedBM  int    `json:"indexed_bm25"`
	IndexedVec int    `json:"indexed_vec"`
}

func handleIngest(w http.ResponseWriter, r *http.Request) {
	var req ingestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Docs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sess := ensureSession(req.SessionID, ttlOrDefault(req.TTLHours))
	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Chunk + upsert
	var chunks []DocChunk
	now := time.Now()
	for _, d := range req.Docs {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		hash := sha1Hex(normalize(d.Text))
		// dedupe by content hash
		already := false
		for _, c := range sess.Meta {
			if c.ContentHash == hash {
				already = true
				break
			}
		}
		if already {
			continue
		}

		for i, part := range makeChunks(d.Text, chunkTokensApprox, chunkOverlapChars) {
			id := fmt.Sprintf("%s#%03d", hash, i)
			ch := DocChunk{
				DocID: id, URL: d.URL, Title: d.Title, Text: part,
				PublishedAt: d.PublishedAt, ContentHash: hash, Lang: d.Lang,
				IngestedAt: now, ChunkIndex: i, SourceSessID: sess.ID,
			}
			chunks = append(chunks, ch)
		}
	}

	// Index to Bleve
	bmCount := 0
	for _, c := range chunks {
		if err := sess.Bleve.Index(c.DocID, c); err == nil {
			bmCount++
		}
		sess.Meta[c.DocID] = c
	}

	// Embeddings (brute-force vectors for small corpora)
	vecCount := 0
	if len(chunks) > 0 {
		vecs, err := embedMany(r.Context(), mapChunksToTexts(chunks))
		if err != nil {
			writeJSON(w, ingestRes{SessionID: sess.ID, Chunks: len(chunks), IndexedBM: bmCount, IndexedVec: 0}, nil)
			return
		}
		for i, v := range vecs {
			sess.Vectors = append(sess.Vectors, embedVec{DocID: chunks[i].DocID, Vec: v})
			vecCount++
		}
	}

	writeJSON(w, ingestRes{SessionID: sess.ID, Chunks: len(chunks), IndexedBM: bmCount, IndexedVec: vecCount}, nil)
}

func ensureSession(id string, ttl time.Duration) *Session {
	globalStore.mu.Lock()
	defer globalStore.mu.Unlock()
	if id != "" {
		if s, ok := globalStore.sessions[id]; ok {
			// bump expiry
			s.ExpiresAt = time.Now().Add(ttl)
			return s
		}
	}
	// create new
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		log.Fatalf("bleve: %v", err)
	}
	s := &Session{
		ID: uuid.NewString(), CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
		Bleve:     index, Vectors: []embedVec{}, Meta: map[string]DocChunk{}, SeenURLs: map[string]time.Time{},
	}
	globalStore.sessions[s.ID] = s
	return s
}

func ttlOrDefault(h int) time.Duration {
	if h <= 0 || h > 7*24 {
		return defaultTTL
	}
	return time.Duration(h) * time.Hour
}

func makeChunks(s string, approx int, overlap int) []string {
	s = strings.TrimSpace(s)
	if len(s) <= approx {
		return []string{s}
	}
	var out []string
	for start := 0; start < len(s); {
		end := start + approx
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[start:end])
		if end == len(s) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return out
}

func mapChunksToTexts(chs []DocChunk) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.Text
	}
	return out
}

func embedMany(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	req := openai.EmbeddingRequest{
		Input: texts,
		Model: openai.EmbeddingModel(modelEmbedding),
	}
	resp, err := openaiClient.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		// Convert []float32-ish from []float32? SDK returns []float32 already
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// ========== SEARCH (hybrid + RRF) ==========

type searchReq struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query"`
	K         int    `json:"k"`
}

type searchRes struct {
	Hits []SearchHit `json:"hits"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.Query) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sess := getSession(req.SessionID)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	k := req.K
	if k <= 0 || k > 50 {
		k = 10
	}

	// BM25
	bmHits := bm25Search(sess, req.Query, k)

	// Vector
	qvecs, err := embedMany(r.Context(), []string{req.Query})
	if err != nil {
		writeJSON(w, searchRes{Hits: bmHits}, nil)
		return
	}
	vecHits := vectorSearch(sess, qvecs[0], k)

	// Fuse
	hits := fuseRRF(bmHits, vecHits, k)
	writeJSON(w, searchRes{Hits: hits}, nil)
}

func bm25Search(sess *Session, q string, k int) []SearchHit {
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	query := bleve.NewQueryStringQuery(q)
	searchReq := bleve.NewSearchRequestOptions(query, k*3, 0, false)
	searchReq.Highlight = bleve.NewHighlightWithStyle("html")
	res, err := sess.Bleve.Search(searchReq)
	if err != nil {
		return nil
	}
	var out []SearchHit
	for i, hit := range res.Hits {
		doc := sess.Meta[hit.ID]
		out = append(out, SearchHit{
			DocID: hit.ID, URL: doc.URL, Title: doc.Title,
			Snippet: snippet(doc.Text),
			Score:   hit.Score, Rank: i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out
}

func vectorSearch(sess *Session, q []float32, k int) []SearchHit {
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	type scored struct {
		id    string
		score float64
	}
	var scoreds []scored
	for _, v := range sess.Vectors {
		s := cosine(q, v.Vec)
		scoreds = append(scoreds, scored{id: v.DocID, score: s})
	}
	sort.Slice(scoreds, func(i, j int) bool { return scoreds[i].score > scoreds[j].score })
	var out []SearchHit
	for i, sc := range scoreds {
		doc := sess.Meta[sc.id]
		out = append(out, SearchHit{
			DocID: sc.id, URL: doc.URL, Title: doc.Title,
			Snippet: snippet(doc.Text), Score: sc.score, Rank: i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out
}

func fuseRRF(a, b []SearchHit, k int) []SearchHit {
	type agg struct {
		item  SearchHit
		score float64
		seen  bool
	}
	m := map[string]*agg{}
	add := func(list []SearchHit) {
		for _, h := range list {
			x, ok := m[h.DocID]
			if !ok {
				m[h.DocID] = &agg{item: h, score: 0, seen: true}
				x = m[h.DocID]
			}
			x.score += 1.0 / float64(rrfK+h.Rank)
		}
	}
	add(a)
	add(b)
	var items []struct {
		SearchHit
		fused float64
	}
	for _, v := range m {
		items = append(items, struct {
			SearchHit
			fused float64
		}{v.item, v.score})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].fused > items[j].fused })
	out := make([]SearchHit, 0, min(k, len(items)))
	for i := 0; i < min(k, len(items)); i++ {
		x := items[i]
		x.SearchHit.Score = x.fused
		x.SearchHit.Rank = i + 1
		out = append(out, x.SearchHit)
	}
	return out
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func snippet(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
}

// ========== SEEN (avoid refetching) ==========

func handleSeen(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	url := r.URL.Query().Get("url")
	if strings.TrimSpace(sid) == "" || strings.TrimSpace(url) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s := getSession(sid)
	if s == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.SeenURLs[url]
	if !ok {
		s.SeenURLs[url] = time.Now()
	}
	writeJSON(w, map[string]any{"seen": ok}, nil)
}

func getSession(id string) *Session {
	globalStore.mu.RLock()
	defer globalStore.mu.RUnlock()
	return globalStore.sessions[id]
}

// ========== SUMMARIZE (LLM with bracketed citations) ==========

type summarizeReq struct {
	Question  string        `json:"question"`
	Contexts  []summContext `json:"contexts"`
	Style     string        `json:"style"` // bullets|paragraph|qa
	MaxTokens int           `json:"max_tokens"`
}
type summContext struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	PublishedAt string `json:"published_at,omitempty"`
}
type summarizeRes struct {
	Answer     string         `json:"answer"`
	Citations  []summCitation `json:"citations"`
	UsedTokens int            `json:"used_tokens"`
}

type summCitation struct {
	ID          int    `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	PublishedAt string `json:"published_at,omitempty"`
}

func handleSummarize(w http.ResponseWriter, r *http.Request) {
	var req summarizeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" || len(req.Contexts) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	style := req.Style
	if style == "" {
		style = "bullets"
	}
	maxToks := req.MaxTokens
	if maxToks <= 0 || maxToks > 1200 {
		maxToks = 800
	}

	system := "You are a precise research assistant. Use ONLY the provided contexts.\n" +
		"Cite facts with bracketed references like [1], [2] that map to the provided URLs.\n" +
		"Prefer newer sources if they conflict, but call out disagreements explicitly.\n" +
		"If something is unknown, say so. No chain-of-thought; only conclusions.\n" +
		fmt.Sprintf("Output style: %s.", style)

	var b strings.Builder
	var cits []summCitation
	for i, c := range req.Contexts {
		fmt.Fprintf(&b, "\n[Source %d] %s\nURL: %s\nText:\n%s\n", i+1, c.Title, c.URL, c.Text)
		cits = append(cits, summCitation{ID: i + 1, URL: c.URL, Title: c.Title, PublishedAt: c.PublishedAt})
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system},
		{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("Question: %s\n\nSources:\n%s\nRespond with citations like [1], [2].", req.Question, b.String())},
	}

	resp, err := openaiClient.CreateChatCompletion(r.Context(), openai.ChatCompletionRequest{
		Model:       modelChat,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   maxToks,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("llm error: %v", err), 502)
		return
	}
	answer := resp.Choices[0].Message.Content
	writeJSON(w, summarizeRes{Answer: answer, Citations: cits, UsedTokens: resp.Usage.TotalTokens}, nil)
}

// ========== HELPERS & JANITOR ==========

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code := http.StatusInternalServerError
		var he httpErr
		if errors.As(err, &he) {
			code = he.code
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

type httpErr struct {
	code int
	msg  string
}

func (e httpErr) Error() string { return e.msg }

func janitor() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		globalStore.mu.Lock()
		for id, s := range globalStore.sessions {
			if time.Now().After(s.ExpiresAt) {
				_ = s.Bleve.Close()
				delete(globalStore.sessions, id)
			}
		}
		globalStore.mu.Unlock()
	}
}

func sha1Hex(s string) string   { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
func normalize(s string) string { return strings.TrimSpace(reSpaces.ReplaceAllString(s, " ")) }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
