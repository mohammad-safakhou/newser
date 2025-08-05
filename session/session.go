package session

import (
	"fmt"
	"github.com/mohammad-safakhou/newser/session/inmemory"
	"github.com/mohammad-safakhou/newser/session/session_models"
	"github.com/mohammad-safakhou/newser/tools/embedding"
	"time"
)

// Store interface for session_object management
type Store interface {
	EnsureSession(id string, ttl time.Duration) (Session, error)
	GetSession(id string) (Session, error)
}

// Session interface for session_object operations
type Session interface {
	ID() string
	Expire(ttl time.Duration)
	AddChunk(chunk session_models.DocChunk) error
	Chunks() []session_models.DocChunk
	Index(id string, data interface{}) error
	SetVector(docID string, v []float32)
	GetVectors() []embedding.EmbedVec
	SetMeta(docID string, meta map[string]session_models.DocChunk)
	GetMeta(docID string) map[string]session_models.DocChunk
	Bm25Search(q string, k int) ([]session_models.SearchHit, error)
	VectorSearch(q []float32, k int) []session_models.SearchHit
	FuseRRF(a, b []session_models.SearchHit, k int) []session_models.SearchHit
}

type StoreType string

const (
	InMemoryStore StoreType = "inmemory"
)

func NewStore(storeType StoreType) Store {
	var store Store
	switch storeType {
	case InMemoryStore:
		store = inmemory.NewInMemorySessionStore()
	default:
		panic(fmt.Sprintf("unsupported store type: %s", storeType))
	}

	return store
}
