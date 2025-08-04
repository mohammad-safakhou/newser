package web_ingest

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/session"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/session/inmemory"
	redis_session "github.com/mohammad-safakhou/newser/tools/web_ingest/session/redis"
	"strings"
	"time"
)

type Ingest struct {
	Store session.Store // Store interface for session management
}

type StoreType string

const (
	InMemoryStore StoreType = "inmemory"
	RedisStore    StoreType = "redis"
)

// NewIngest creates a new Ingest instance with the provided session store.
func NewIngest(storeType StoreType) *Ingest {
	var store session.Store
	switch storeType {
	case InMemoryStore:
		store = inmemory.NewInMemorySessionStore()
	case RedisStore:
		store = redis_session.NewRedisSessionStore(
			fmt.Sprintf("%s:%s", config.AppConfig.Databases.Redis.Host, config.AppConfig.Databases.Redis.Port),
			config.AppConfig.Databases.Redis.Pass,
			config.AppConfig.Databases.Redis.DB)
	default:
		panic(fmt.Sprintf("unsupported store type: %s", storeType))
	}
	return &Ingest{Store: store}
}

func (i Ingest) Ingest(sessionID string, docs []models.DocInput, ttlsHours int) (models.IngestResponse, error) {
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

	var chunks []models.DocChunk
	now := time.Now()
	for _, doc := range docs {
		if strings.TrimSpace(doc.Text) == "" {
			continue
		}
		hash := sha1Hex(doc.Text)
		for i, part := range makeChunks(doc.Text, 1000, 200) {
			chunk := models.DocChunk{
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

	return models.IngestResponse{
		SessionID: sess.ID(),
		Chunks:    len(chunks),
		IndexedBM: len(chunks),
	}, nil
}

// Utility functions (can be moved to a utils package)
func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
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
