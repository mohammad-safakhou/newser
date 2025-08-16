// Package session provides corpus/session models and a Redis-backed Store.
// It persists all chunks, vectors and metadata in Redis, and lazily rebuilds
// an in-memory Bleve index per session on first use.
//
// Design notes:
//   - We DO NOT try to persist Bleve internals. Instead, we persist the raw
//     corpus (chunks & vectors) to Redis and rebuild an in-memory index
//     deterministically when needed.
//   - All keys are namespaced by session ID and get the same TTL. Restarting
//     MCP/agents won't lose data as long as Redis keeps the keys alive.
//
// Keyspace (all keys get TTL):
//
//	sess:<sid>:meta                 "1" (presence marker)
//	sess:<sid>:ttl_seconds          "<int>" (optional, last used ttl)
//	sess:<sid>:doc_ids              SET(docID)
//	sess:<sid>:doc:<docID>          HASH{ url,title,text,published_at,content_hash,
//	                                     ingested_at,chunk_index,source_sess_id }
//	sess:<sid>:vec:<docID>          STRING base64(little-endian float32 array)
package session

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//
// ===========================
// Models & Interfaces (API)
// ===========================
//

// Corpus is the minimum API used by ingest/search tools.
type Corpus interface {
	ID() string

	// Indexing API
	AddChunk(c DocChunk) error
	Index(id string, doc any) error
	SetVector(id string, vec []float32)

	// Retrieval API
	Bm25Search(q string, k int) ([]SearchHit, error)
	VectorSearch(qvec []float32, k int) []SearchHit
	FuseRRF(bm, vec []SearchHit, k int) []SearchHit
}

// DocChunk is an indexed piece of a document.
type DocChunk struct {
	DocID        string
	URL          string
	Title        string
	Text         string
	PublishedAt  string
	ContentHash  string
	IngestedAt   time.Time
	ChunkIndex   int
	SourceSessID string
}

// DocInput is a raw document provided to ingest (pre-chunking).
type DocInput struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	PublishedAt string `json:"published_at,omitempty"`
}

// SearchHit is a result entry with fused/individual scores.
type SearchHit struct {
	DocID   string  `json:"doc_id"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Rank    int     `json:"rank"`
}

// Store creates/opens sessions and controls their lifetime.
type Store interface {
	EnsureSession(id string, ttl time.Duration) (Session, error)
	GetSession(id string) (Session, error)
	// Optional cleanup for tests / admin:
	DeleteSession(id string) error
}

// Session is a single corpus handle.
type Session interface {
	ID() string
	Expire(ttl time.Duration)

	AddChunk(chunk DocChunk) error
	Chunks() []DocChunk

	Index(id string, data interface{}) error

	SetVector(docID string, v []float32)
	GetVectors() []EmbedVec

	SetMeta(docID string, meta map[string]DocChunk)
	GetMeta(docID string) map[string]DocChunk

	Bm25Search(q string, k int) ([]SearchHit, error)
	VectorSearch(q []float32, k int) []SearchHit
	FuseRRF(a, b []SearchHit, k int) []SearchHit
}

// EmbedVec binds a vector to a doc chunk ID.
type EmbedVec struct {
	DocID string
	Vec   []float32
}

//
// ====================================
// Redis-backed Store & Session structs
// ====================================
//

const rrfK = 60 // reciprocal rank fusion constant

// RedisStore implements Store using Redis.
type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore constructs a Redis-backed Store.
//
// addr example: "127.0.0.1:6379"
func NewRedisStore(host, port, password string, db int) *RedisStore {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
		DB:       db,
	})
	return &RedisStore{rdb: rdb}
}

// EnsureSession creates or touches a session and applies TTL to base keys.
// If id == "", a new UUID is generated. Returns a handle that lazily builds
// its Bleve index from Redis when first searched/used.
func (s *RedisStore) EnsureSession(id string, ttl time.Duration) (Session, error) {
	ctx := context.Background()
	if strings.TrimSpace(id) == "" {
		id = uuid.NewString()
	}
	pipe := s.rdb.TxPipeline()
	pipe.SetNX(ctx, rk(id, "meta"), "1", ttl)
	pipe.Set(ctx, rk(id, "ttl_seconds"), strconv.Itoa(int(ttl.Seconds())), ttl)
	pipe.Expire(ctx, rk(id, "meta"), ttl)
	pipe.Expire(ctx, rk(id, "doc_ids"), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return newRedisSession(s.rdb, id, ttl), nil
}

// GetSession returns a session handle; it may represent an empty corpus if
// keys expired. Search & accessors handle that gracefully.
func (s *RedisStore) GetSession(id string) (Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("empty session id")
	}
	return newRedisSession(s.rdb, id, 0), nil
}

// DeleteSession removes all keys for a session (best effort).
func (s *RedisStore) DeleteSession(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("empty session id")
	}
	ctx := context.Background()
	docIDs, _ := s.rdb.SMembers(ctx, rk(id, "doc_ids")).Result()
	keys := []string{
		rk(id, "meta"),
		rk(id, "ttl_seconds"),
		rk(id, "doc_ids"),
	}
	for _, d := range docIDs {
		keys = append(keys, rk(id, "doc", d), rk(id, "vec", d))
	}
	if len(keys) == 0 {
		return nil
	}
	return s.rdb.Del(ctx, keys...).Err()
}

// redisSession is a lazily indexed session backed by Redis.
type redisSession struct {
	rdb       *redis.Client
	id        string
	mu        sync.RWMutex
	expiresAt time.Time
	ttlHint   time.Duration

	bleve   bleve.Index
	meta    map[string]DocChunk
	vectors []EmbedVec

	indexed bool // bleve built
	loadedM bool // meta loaded
	loadedV bool // vectors loaded
}

func newRedisSession(rdb *redis.Client, id string, ttl time.Duration) *redisSession {
	return &redisSession{
		rdb:     rdb,
		id:      id,
		ttlHint: ttl,
		meta:    make(map[string]DocChunk),
	}
}

func (s *redisSession) ID() string { return s.id }

// currentTTL returns the TTL we should apply to keys for this session.
// Precedence:
//  1. s.ttlHint if set by EnsureSession/Expire
//  2. value from Redis key sess:<sid>:ttl_seconds (if present, >0)
//  3. a sane default (48h) to avoid immortal keys if nothing else is available.
func (s *redisSession) currentTTL(ctx context.Context) time.Duration {
	// 1) in-memory hint
	if s.ttlHint > 0 {
		return s.ttlHint
	}

	// 2) try reading from Redis
	if s.rdb != nil {
		if v, err := s.rdb.Get(ctx, rk(s.id, "ttl_seconds")).Result(); err == nil && v != "" {
			if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}

	// 3) fallback default
	return 48 * time.Hour
}

// Expire updates TTL across top-level keys; per-doc keys are touched on access/write.
func (s *redisSession) Expire(ttl time.Duration) {
	ctx := context.Background()
	s.mu.Lock()
	s.expiresAt = time.Now().Add(ttl)
	s.ttlHint = ttl
	s.mu.Unlock()

	pipe := s.rdb.TxPipeline()
	pipe.Expire(ctx, rk(s.id, "meta"), ttl)
	pipe.Expire(ctx, rk(s.id, "doc_ids"), ttl)
	pipe.Set(ctx, rk(s.id, "ttl_seconds"), strconv.Itoa(int(ttl.Seconds())), ttl)
	pipe.Expire(ctx, rk(s.id, "ttl_seconds"), ttl)
	_, _ = pipe.Exec(ctx)
}

// AddChunk persists a chunk and indexes it into the in-memory bleve.
func (s *redisSession) AddChunk(chunk DocChunk) error {
	if strings.TrimSpace(chunk.DocID) == "" {
		return errors.New("chunk.DocID required")
	}
	ctx := context.Background()
	ttl := s.currentTTL(ctx)

	docKey := rk(s.id, "doc", chunk.DocID)
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, docKey, map[string]any{
		"url":            chunk.URL,
		"title":          chunk.Title,
		"text":           chunk.Text,
		"published_at":   chunk.PublishedAt,
		"content_hash":   chunk.ContentHash,
		"ingested_at":    chunk.IngestedAt.UTC().Format(time.RFC3339Nano),
		"chunk_index":    chunk.ChunkIndex,
		"source_sess_id": chunk.SourceSessID,
	})
	pipe.SAdd(ctx, rk(s.id, "doc_ids"), chunk.DocID)
	pipe.Expire(ctx, docKey, ttl)
	pipe.Expire(ctx, rk(s.id, "doc_ids"), ttl)
	pipe.Expire(ctx, rk(s.id, "meta"), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bleve == nil {
		idx, err := bleve.NewMemOnly(bleve.NewIndexMapping())
		if err != nil {
			return err
		}
		s.bleve = idx
	}
	s.meta[chunk.DocID] = chunk
	if err := s.bleve.Index(chunk.DocID, chunk); err != nil {
		return fmt.Errorf("bleve index: %w", err)
	}
	s.indexed = true
	s.loadedM = true
	return nil
}

func (s *redisSession) Chunks() []DocChunk {
	s.ensureMeta()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DocChunk, 0, len(s.meta))
	for _, c := range s.meta {
		out = append(out, c)
	}
	return out
}

// Index arbitrary data into bleve (not persisted separately).
func (s *redisSession) Index(id string, data interface{}) error {
	s.ensureIndex()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bleve == nil {
		return errors.New("bleve not initialized")
	}
	return s.bleve.Index(id, data)
}

// SetVector persists (base64) and caches a vector.
func (s *redisSession) SetVector(docID string, v []float32) {
	ctx := context.Background()
	ttl := s.currentTTL(ctx)
	key := rk(s.id, "vec", docID)
	enc := encodeVec(v)

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, key, enc, ttl)
	pipe.Expire(ctx, rk(s.id, "meta"), ttl)
	_, _ = pipe.Exec(ctx)

	s.mu.Lock()
	s.vectors = append(s.vectors, EmbedVec{DocID: docID, Vec: append([]float32(nil), v...)})
	s.loadedV = true
	s.mu.Unlock()
}

// GetVectors returns cached vectors; loads from Redis if needed.
func (s *redisSession) GetVectors() []EmbedVec {
	s.ensureVectors()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EmbedVec, len(s.vectors))
	copy(out, s.vectors)
	return out
}

// SetMeta updates local meta for a single docID (compat helper).
func (s *redisSession) SetMeta(docID string, meta map[string]DocChunk) {
	if meta == nil {
		return
	}
	s.mu.Lock()
	if v, ok := meta[docID]; ok {
		s.meta[docID] = v
	}
	s.mu.Unlock()
}

func (s *redisSession) GetMeta(docID string) map[string]DocChunk {
	s.ensureMeta()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.meta[docID]; ok {
		return map[string]DocChunk{docID: v}
	}
	return nil
}

func (s *redisSession) Bm25Search(q string, k int) ([]SearchHit, error) {
	if k <= 0 {
		k = 10
	}
	s.ensureIndex()

	s.mu.RLock()
	idx := s.bleve
	s.mu.RUnlock()
	if idx == nil {
		return nil, nil
	}

	query := bleve.NewQueryStringQuery(q)
	req := bleve.NewSearchRequestOptions(query, k*3, 0, false)
	req.Highlight = bleve.NewHighlightWithStyle("html")
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SearchHit, 0, min(k, len(res.Hits)))
	for i, hit := range res.Hits {
		doc := s.meta[hit.ID]
		out = append(out, SearchHit{
			DocID:   hit.ID,
			URL:     doc.URL,
			Title:   doc.Title,
			Snippet: snippet(doc.Text),
			Score:   hit.Score,
			Rank:    i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out, nil
}

func (s *redisSession) VectorSearch(q []float32, k int) []SearchHit {
	if k <= 0 {
		k = 10
	}
	s.ensureMeta()
	s.ensureVectors()

	s.mu.RLock()
	defer s.mu.RUnlock()
	type scored struct {
		id    string
		score float64
	}
	var scoreds []scored
	for _, v := range s.vectors {
		scoreds = append(scoreds, scored{id: v.DocID, score: cosine(q, v.Vec)})
	}
	sort.Slice(scoreds, func(i, j int) bool { return scoreds[i].score > scoreds[j].score })
	out := make([]SearchHit, 0, min(k, len(scoreds)))
	for i, sc := range scoreds {
		doc := s.meta[sc.id]
		out = append(out, SearchHit{
			DocID:   sc.id,
			URL:     doc.URL,
			Title:   doc.Title,
			Snippet: snippet(doc.Text),
			Score:   sc.score,
			Rank:    i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out
}

func (s *redisSession) FuseRRF(a, b []SearchHit, k int) []SearchHit {
	type agg struct {
		item  SearchHit
		score float64
	}
	m := map[string]*agg{}
	add := func(list []SearchHit) {
		for _, h := range list {
			x, ok := m[h.DocID]
			if !ok {
				m[h.DocID] = &agg{item: h}
				x = m[h.DocID]
			}
			x.score += 1.0 / float64(rrfK+h.Rank)
		}
	}
	add(a)
	add(b)
	items := make([]struct {
		SearchHit
		fused float64
	}, 0, len(m))
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

//
// ===============
// Lazy loaders
// ===============
//

func (s *redisSession) ensureIndex() {
	// Fast path
	s.mu.RLock()
	ok := s.indexed && s.bleve != nil && s.loadedM
	s.mu.RUnlock()
	if ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexed && s.bleve != nil && s.loadedM {
		return
	}
	idx, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return
	}
	meta, _ := s.loadAllDocs(context.Background())
	for id, c := range meta {
		_ = idx.Index(id, c) // best effort
	}
	s.meta = meta
	s.bleve = idx
	s.indexed = true
	s.loadedM = true
}

func (s *redisSession) ensureMeta() {
	s.mu.RLock()
	loaded := s.loadedM
	s.mu.RUnlock()
	if loaded {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadedM {
		return
	}
	meta, _ := s.loadAllDocs(context.Background())
	s.meta = meta
	s.loadedM = true
}

func (s *redisSession) ensureVectors() {
	s.mu.RLock()
	loaded := s.loadedV
	s.mu.RUnlock()
	if loaded {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadedV {
		return
	}
	vecs, _ := s.loadAllVecs(context.Background())
	s.vectors = vecs
	s.loadedV = true
}

// loadAllDocs pulls every doc hash for this session from Redis.
func (s *redisSession) loadAllDocs(ctx context.Context) (map[string]DocChunk, error) {
	out := make(map[string]DocChunk)
	docIDs, err := s.rdb.SMembers(ctx, rk(s.id, "doc_ids")).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return out, err
	}
	for _, id := range docIDs {
		h, err := s.rdb.HGetAll(ctx, rk(s.id, "doc", id)).Result()
		if err != nil || len(h) == 0 {
			continue
		}
		chunk := DocChunk{
			DocID:        id,
			URL:          h["url"],
			Title:        h["title"],
			Text:         h["text"],
			PublishedAt:  h["published_at"],
			ContentHash:  h["content_hash"],
			SourceSessID: h["source_sess_id"],
		}
		if v := h["ingested_at"]; v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				chunk.IngestedAt = t
			}
		}
		if v := h["chunk_index"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				chunk.ChunkIndex = n
			}
		}
		out[id] = chunk

		// best-effort ttl touch
		if s.ttlHint > 0 {
			s.rdb.Expire(ctx, rk(s.id, "doc", id), s.ttlHint)
		}
	}
	if s.ttlHint > 0 {
		pipe := s.rdb.TxPipeline()
		pipe.Expire(ctx, rk(s.id, "doc_ids"), s.ttlHint)
		pipe.Expire(ctx, rk(s.id, "meta"), s.ttlHint)
		_, _ = pipe.Exec(ctx)
	}
	return out, nil
}

// loadAllVecs pulls all vectors for this session from Redis.
func (s *redisSession) loadAllVecs(ctx context.Context) ([]EmbedVec, error) {
	var out []EmbedVec
	docIDs, err := s.rdb.SMembers(ctx, rk(s.id, "doc_ids")).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return out, err
	}
	for _, id := range docIDs {
		raw, err := s.rdb.Get(ctx, rk(s.id, "vec", id)).Bytes()
		if err != nil {
			continue
		}
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))
		n, err := base64.StdEncoding.Decode(decoded, raw)
		if err != nil {
			continue
		}
		vec := decodeVec(decoded[:n])
		out = append(out, EmbedVec{DocID: id, Vec: vec})

		if s.ttlHint > 0 {
			s.rdb.Expire(ctx, rk(s.id, "vec", id), s.ttlHint)
		}
	}
	return out, nil
}

//
// ===============
// Helpers/Utils
// ===============
//

func rk(sid string, parts ...string) string {
	// "sess:<sid>:<p1>:<p2>..."
	var sb strings.Builder
	sb.Grow(6 + len(sid) + 8*len(parts))
	sb.WriteString("sess:")
	sb.WriteString(sid)
	for _, p := range parts {
		sb.WriteByte(':')
		sb.WriteString(p)
	}
	return sb.String()
}

func encodeVec(v []float32) string {
	b := make([]byte, 4*len(v))
	for i := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v[i]))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func decodeVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		u := binary.LittleEndian.Uint32(b[4*i:])
		out[i] = math.Float32frombits(u)
	}
	return out
}

func snippet(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
