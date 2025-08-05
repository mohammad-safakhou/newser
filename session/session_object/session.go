package session_models

import (
	"github.com/blevesearch/bleve"
	"github.com/mohammad-safakhou/newser/session/session_models"
	"github.com/mohammad-safakhou/newser/tools/embedding"
	"math"
	"sort"
	"sync"
	"time"
)

type Session struct {
	id        string
	expiresAt time.Time
	bleve     bleve.Index
	meta      map[string]session_models.DocChunk
	vectors   []embedding.EmbedVec // in-memory vectors for small corpora
	mu        sync.RWMutex
}

const rrfK = 60 // reciprocal-rank-fusion constant

func (s *Session) ID() string               { return s.id }
func (s *Session) Expire(ttl time.Duration) { s.expiresAt = time.Now().Add(ttl) }
func (s *Session) AddChunk(chunk session_models.DocChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[chunk.DocID] = chunk
	return s.bleve.Index(chunk.DocID, chunk)
}
func (s *Session) Chunks() []session_models.DocChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]session_models.DocChunk, 0, len(s.meta))
	for _, c := range s.meta {
		out = append(out, c)
	}
	return out
}
func (s *Session) Index(id string, data interface{}) error {
	return s.bleve.Index(id, data)
}

func (s *Session) SetVector(docID string, v []float32) {
	s.vectors = append(s.vectors, embedding.EmbedVec{DocID: docID, Vec: v})
}

func (s *Session) GetVectors() []embedding.EmbedVec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vectors
}

func (s *Session) SetMeta(docID string, meta map[string]session_models.DocChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta == nil {
		s.meta = make(map[string]session_models.DocChunk)
	}
	s.meta[docID] = meta[docID]
}

func (s *Session) GetMeta(docID string) map[string]session_models.DocChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.meta == nil {
		return nil
	}
	meta, exists := s.meta[docID]
	if !exists {
		return nil
	}
	return map[string]session_models.DocChunk{docID: meta}
}

func (s *Session) Bm25Search(q string, k int) ([]session_models.SearchHit, error) {
	query := bleve.NewQueryStringQuery(q)
	searchReq := bleve.NewSearchRequestOptions(query, k*3, 0, false)
	searchReq.Highlight = bleve.NewHighlightWithStyle("html")
	res, err := s.bleve.Search(searchReq)
	if err != nil {
		return nil, err
	}
	var out []session_models.SearchHit
	for i, hit := range res.Hits {
		doc := s.meta[hit.ID]
		out = append(out, session_models.SearchHit{
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

func (s *Session) VectorSearch(q []float32, k int) []session_models.SearchHit {
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
	var out []session_models.SearchHit
	for i, sc := range scoreds {
		doc := s.meta[sc.id]
		out = append(out, session_models.SearchHit{
			DocID: sc.id, URL: doc.URL, Title: doc.Title,
			Snippet: snippet(doc.Text), Score: sc.score, Rank: i + 1,
		})
		if len(out) >= k {
			break
		}
	}
	return out
}

func (s *Session) FuseRRF(a, b []session_models.SearchHit, k int) []session_models.SearchHit {
	type agg struct {
		item  session_models.SearchHit
		score float64
		seen  bool
	}
	m := map[string]*agg{}
	add := func(list []session_models.SearchHit) {
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
		session_models.SearchHit
		fused float64
	}
	for _, v := range m {
		items = append(items, struct {
			session_models.SearchHit
			fused float64
		}{v.item, v.score})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].fused > items[j].fused })
	out := make([]session_models.SearchHit, 0, min(k, len(items)))
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

func NewSession(id string, ttl time.Duration) (*Session, error) {
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	return &Session{
		id:        id,
		expiresAt: time.Now().Add(ttl),
		bleve:     index,
		meta:      make(map[string]session_models.DocChunk),
		vectors:   []embedding.EmbedVec{},
	}, nil
}

func snippet(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
}
