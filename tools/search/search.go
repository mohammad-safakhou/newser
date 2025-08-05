package search

import (
	"context"
	"github.com/mohammad-safakhou/newser/session"
	"github.com/mohammad-safakhou/newser/session/session_models"
	"github.com/mohammad-safakhou/newser/tools/embedding"
)

type Search struct {
	Store     session.Store // Store interface for session_object management
	Embedding embedding.Embedding
}

func NewSearch(store session.Store) Search {
	return Search{Store: store}
}

func (s Search) Search(sessionID string, q string, k int) ([]session_models.SearchHit, error) {
	sess, err := s.Store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	if k <= 0 || k > 50 {
		k = 10
	}

	// BM25
	bmHits, err := sess.Bm25Search(q, k)
	if err != nil {
		return nil, err
	}

	// Vector
	qvecs, err := s.Embedding.EmbedMany(context.Background(), []string{q})
	if err != nil {
		return nil, err
	}
	vecHits := sess.VectorSearch(qvecs[0], k)

	// Fuse
	hits := sess.FuseRRF(bmHits, vecHits, k)

	return hits, nil
}
