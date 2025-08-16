// Package web_ingest: pure ingestion helpers operating on a provided Corpus.
package web_ingest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/mcp/tools/embedding"
	"github.com/mohammad-safakhou/newser/session"
)

type IngestResponse struct {
	SessionID  string `json:"session_id"`
	Chunks     int    `json:"chunks"`
	IndexedBM  int    `json:"indexed_bm25"`
	IndexedVec int    `json:"indexed_vec"`
}

// Ingestor is stateless; it embeds with the provided Embedding and writes to the provided Corpus.
type Ingestor struct {
	Embedding   embedding.Embedding
	ApproxChunk int
	Overlap     int
}

// NewIngestor constructs an ingestor with chunking defaults if zeros are provided.
func NewIngestor(emb embedding.Embedding, approx, overlap int) *Ingestor {
	if approx <= 0 {
		approx = 1000
	}
	if overlap < 0 {
		overlap = 200
	}
	return &Ingestor{Embedding: emb, ApproxChunk: approx, Overlap: overlap}
}

// DocInput mirrors your session.DocInput to avoid a hard dependency on session.
type DocInput struct {
	URL         string
	Title       string
	Text        string
	PublishedAt string
}

// IngestDocs chunks, indexes, and vectors docs into corp.
// *No* session/store lookup here; the caller supplies a ready Corpus.
// Empty texts are skipped. Returns counts for observability.
func (ig Ingestor) IngestDocs(ctx context.Context, corp session.Corpus, docs []DocInput) (IngestResponse, error) {
	if corp == nil {
		return IngestResponse{}, errors.New("ingest: nil corpus")
	}
	if len(docs) == 0 {
		return IngestResponse{}, errors.New("ingest: no documents provided")
	}

	var chunks []session.DocChunk
	now := time.Now()

	for _, d := range docs {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		hash := sha1Hex(d.Text)
		parts := makeChunks(d.Text, ig.ApproxChunk, ig.Overlap)
		for i, part := range parts {
			ch := session.DocChunk{
				DocID:        fmt.Sprintf("%s#%03d", hash, i),
				URL:          d.URL,
				Title:        d.Title,
				Text:         part,
				PublishedAt:  d.PublishedAt,
				ContentHash:  hash,
				IngestedAt:   now,
				ChunkIndex:   i,
				SourceSessID: corp.ID(),
			}
			if err := corp.AddChunk(ch); err != nil {
				return IngestResponse{}, fmt.Errorf("add chunk: %w", err)
			}
			if err := corp.Index(ch.DocID, ch); err != nil {
				return IngestResponse{}, fmt.Errorf("index: %w", err)
			}
			chunks = append(chunks, ch)
		}
	}

	vecCount := 0
	if len(chunks) > 0 {
		vecs, err := ig.Embedding.EmbedMany(ctx, mapChunksToTexts(chunks))
		if err != nil {
			return IngestResponse{}, fmt.Errorf("embedding: %w", err)
		}
		for i, v := range vecs {
			corp.SetVector(chunks[i].DocID, v)
			vecCount++
		}
	}

	return IngestResponse{
		SessionID:  corp.ID(),
		Chunks:     len(chunks),
		IndexedBM:  len(chunks),
		IndexedVec: vecCount,
	}, nil
}

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
