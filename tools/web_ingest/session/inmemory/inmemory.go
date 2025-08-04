package inmemory

import (
	"github.com/blevesearch/bleve"
	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/session"
	"sync"
	"time"
)

type Store struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewInMemorySessionStore() session.Store {
	return &Store{sessions: make(map[string]*Session)}
}

func (store *Store) EnsureSession(id string, ttl time.Duration) (session.Session, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if id != "" {
		if sess, ok := store.sessions[id]; ok {
			sess.Expire(ttl)
			return sess, nil
		}
	}
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	sess := &Session{
		id:        uuid.NewString(),
		expiresAt: time.Now().Add(ttl),
		bleve:     index,
		meta:      make(map[string]models.DocChunk),
	}
	store.sessions[sess.id] = sess
	return sess, nil
}

func (store *Store) GetSession(id string) (session.Session, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	sess, ok := store.sessions[id]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

type Session struct {
	id        string
	expiresAt time.Time
	bleve     bleve.Index
	meta      map[string]models.DocChunk
	mu        sync.RWMutex
}

func (s *Session) ID() string               { return s.id }
func (s *Session) Expire(ttl time.Duration) { s.expiresAt = time.Now().Add(ttl) }
func (s *Session) AddChunk(chunk models.DocChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[chunk.DocID] = chunk
	return s.bleve.Index(chunk.DocID, chunk)
}
func (s *Session) Chunks() []models.DocChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.DocChunk, 0, len(s.meta))
	for _, c := range s.meta {
		out = append(out, c)
	}
	return out
}
