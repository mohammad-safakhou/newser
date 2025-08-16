package web_ingest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/session"
	"github.com/mohammad-safakhou/newser/tools/embedding"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
)

type Ingest struct {
	Store     session.Store // Store interface for session_object management
	Embedding embedding.Embedding
}

// NewIngest creates a new Ingest instance with the provided session_object store.
func NewIngest(store session.Store, embedding embedding.Embedding) *Ingest {
	return &Ingest{Store: store, Embedding: embedding}
}

func (i Ingest) Ingest(sessionID string, docs []session.DocInput, ttlsHours int) (models.IngestResponse, error) {
	if len(docs) == 0 {
		return models.IngestResponse{}, errors.New("no documents provided")
	}
	ttl := 48 * time.Hour
	if ttlsHours > 0 {
		ttl = time.Duration(ttlsHours) * time.Hour
	}
	sess, err := i.Store.EnsureSession(sessionID, ttl)
	if err != nil {
		return models.IngestResponse{}, err
	}

	var chunks []session.DocChunk
	now := time.Now()
	for _, doc := range docs {
		if strings.TrimSpace(doc.Text) == "" {
			continue
		}
		hash := sha1Hex(doc.Text)
		for i, part := range makeChunks(doc.Text, 1000, 200) {
			chunk := session.DocChunk{
				DocID:        fmt.Sprintf("%s#%03d", hash, i),
				URL:          doc.URL,
				Title:        doc.Title,
				Text:         part,
				PublishedAt:  doc.PublishedAt,
				ContentHash:  hash,
				IngestedAt:   now,
				ChunkIndex:   i,
				SourceSessID: sess.ID(),
			}
			err = sess.AddChunk(chunk)
			if err != nil {
				return models.IngestResponse{}, fmt.Errorf("failed to add chunk: %w", err)
			}
			chunks = append(chunks, chunk)
		}
	}

	// Index to Bleve
	bmCount := 0
	for _, c := range chunks {
		if err := sess.Index(c.DocID, c); err == nil {
			bmCount++
		}
	}

	// Embeddings (brute-force vectors for small corpora)
	vecCount := 0
	if len(chunks) > 0 {
		ctx := context.Background()
		vecs, err := i.Embedding.EmbedMany(ctx, mapChunksToTexts(chunks))
		if err != nil {
			return models.IngestResponse{}, fmt.Errorf("embedding error: %w", err)
		}
		for i, v := range vecs {
			sess.SetVector(chunks[i].DocID, v)
			vecCount++
		}
	}

	return models.IngestResponse{
		SessionID:  sess.ID(),
		Chunks:     len(chunks),
		IndexedBM:  bmCount,
		IndexedVec: vecCount,
	}, nil
}

// Utility functions (can be moved to a utils package)
func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func mapChunksToTexts(chs []session.DocChunk) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.Text
	}
	return out
}

func makeChunks(text string, approx, overlap int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= approx {
		return []string{text}
	}
	var chunks []string
	for start := 0; start < len(text); {
		end := start + approx
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[start:end])
		if end == len(text) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return chunks
}
