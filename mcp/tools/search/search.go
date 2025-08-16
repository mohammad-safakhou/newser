package search

import (
	"context"
	"errors"

	"github.com/mohammad-safakhou/newser/mcp/tools/embedding"
	"github.com/mohammad-safakhou/newser/session"
)

// Hybrid performs BM25 + vector search over a caller-provided Corpus.
// It is *stateless*: it doesn’t fetch sessions or persist anything.
type Hybrid struct {
	Embedding embedding.Embedding
}

// NewHybrid constructs a Hybrid search helper.
func NewHybrid(emb embedding.Embedding) Hybrid { return Hybrid{Embedding: emb} }

// Search runs BM25 and vector search over corp and fuses the results.
// - corp: required corpus (session) to search in
// - q:    non-empty natural language query
// - k:    desired number of results (clamped to [1,50])
func (h Hybrid) Search(ctx context.Context, corp session.Corpus, q string, k int) ([]session.SearchHit, error) {
	if corp == nil {
		return nil, errors.New("hybrid search: nil corpus")
	}
	if len(q) == 0 {
		return nil, errors.New("hybrid search: empty query")
	}
	if k < 1 || k > 50 {
		k = 10
	}

	bmHits, err := corp.Bm25Search(q, k)
	if err != nil {
		return nil, err
	}

	qvecs, err := h.Embedding.EmbedMany(ctx, []string{q})
	if err != nil {
		return nil, err
	}
	if len(qvecs) == 0 {
		// Embedding provider returned nothing: fall back to BM25 only
		return bmHits, nil
	}
	vecHits := corp.VectorSearch(qvecs[0], k)
	return corp.FuseRRF(bmHits, vecHits, k), nil
}
