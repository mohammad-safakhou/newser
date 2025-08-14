package session

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/tools/embedding"
)

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

type DocInput struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	PublishedAt string `json:"published_at,omitempty"`
}

type SearchHit struct {
	DocID   string  `json:"doc_id"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Rank    int     `json:"rank"`
}

// Store interface for session_object management
type Store interface {
	EnsureSession(id string, ttl time.Duration) (Session, error)
	GetSession(id string) (Session, error)
}

type store struct {
	sessions map[string]Session
	mu       sync.RWMutex
}

func NewSessionStore() Store {
	return &store{sessions: make(map[string]Session)}
}

func (store *store) EnsureSession(id string, ttl time.Duration) (Session, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if id != "" {
		if sess, ok := store.sessions[id]; ok {
			sess.Expire(ttl)
			return sess, nil
		}
	}

	sess, err := NewSession(uuid.NewString(), ttl)
	if err != nil {
		return nil, err
	}

	store.sessions[sess.ID()] = sess
	return sess, nil
}

func (store *store) GetSession(id string) (Session, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	sess, ok := store.sessions[id]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

// session interface for session_object operations
type Session interface {
	ID() string
	Expire(ttl time.Duration)
	AddChunk(chunk DocChunk) error
	Chunks() []DocChunk
	Index(id string, data interface{}) error
	SetVector(docID string, v []float32)
	GetVectors() []embedding.EmbedVec
	SetMeta(docID string, meta map[string]DocChunk)
	GetMeta(docID string) map[string]DocChunk
	Bm25Search(q string, k int) ([]SearchHit, error)
	VectorSearch(q []float32, k int) []SearchHit
	FuseRRF(a, b []SearchHit, k int) []SearchHit
}

type StoreType string

const (
	InMemoryStore StoreType = "store"
)

func NewStore(storeType StoreType) Store {
	var s Store
	switch storeType {
	case InMemoryStore:
		s = NewSessionStore()
	default:
		panic(fmt.Sprintf("unsupported store type: %s", storeType))
	}

	return s
}

type session struct {
	id        string
	expiresAt time.Time
	bleve     bleve.Index
	meta      map[string]DocChunk
	vectors   []embedding.EmbedVec // in-memory vectors for small corpora
	mu        sync.RWMutex
}

const rrfK = 60 // reciprocal-rank-fusion constant

func (s *session) ID() string               { return s.id }
func (s *session) Expire(ttl time.Duration) { s.expiresAt = time.Now().Add(ttl) }
func (s *session) AddChunk(chunk DocChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[chunk.DocID] = chunk
	return s.bleve.Index(chunk.DocID, chunk)
}
func (s *session) Chunks() []DocChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DocChunk, 0, len(s.meta))
	for _, c := range s.meta {
		out = append(out, c)
	}
	return out
}
func (s *session) Index(id string, data interface{}) error {
	return s.bleve.Index(id, data)
}

func (s *session) SetVector(docID string, v []float32) {
	s.vectors = append(s.vectors, embedding.EmbedVec{DocID: docID, Vec: v})
}

func (s *session) GetVectors() []embedding.EmbedVec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vectors
}

func (s *session) SetMeta(docID string, meta map[string]DocChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta == nil {
		s.meta = make(map[string]DocChunk)
	}
	s.meta[docID] = meta[docID]
}

func (s *session) GetMeta(docID string) map[string]DocChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.meta == nil {
		return nil
	}
	meta, exists := s.meta[docID]
	if !exists {
		return nil
	}
	return map[string]DocChunk{docID: meta}
}

func (s *session) Bm25Search(q string, k int) ([]SearchHit, error) {
	query := bleve.NewQueryStringQuery(q)
	searchReq := bleve.NewSearchRequestOptions(query, k*3, 0, false)
	searchReq.Highlight = bleve.NewHighlightWithStyle("html")
	res, err := s.bleve.Search(searchReq)
	if err != nil {
		return nil, err
	}
	var out []SearchHit
	for i, hit := range res.Hits {
		doc := s.meta[hit.ID]
		out = append(out, SearchHit{
			DocID: hit.ID, URL: doc.URL, Title: doc.Title,
			Snippet: snippet(doc.Text),
			Score:   hit.Score, Rank: i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out, nil
}

func (s *session) VectorSearch(q []float32, k int) []SearchHit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type scored struct {
		id    string
		score float64
	}
	var scoreds []scored
	for _, v := range s.vectors {
		s := cosine(q, v.Vec)
		scoreds = append(scoreds, scored{id: v.DocID, score: s})
	}
	sort.Slice(scoreds, func(i, j int) bool { return scoreds[i].score > scoreds[j].score })
	var out []SearchHit
	for i, sc := range scoreds {
		doc := s.meta[sc.id]
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

func (s *session) FuseRRF(a, b []SearchHit, k int) []SearchHit {
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

func NewSession(id string, ttl time.Duration) (Session, error) {
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	return &session{
		id:        id,
		expiresAt: time.Now().Add(ttl),
		bleve:     index,
		meta:      make(map[string]DocChunk),
		vectors:   []embedding.EmbedVec{},
	}, nil
}

func snippet(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
}
