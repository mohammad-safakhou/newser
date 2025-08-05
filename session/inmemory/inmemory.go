package inmemory

import (
	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/session"
	"github.com/mohammad-safakhou/newser/session/session_object"
	"sync"
	"time"
)

type Store struct {
	sessions map[string]*session_object.Session
	mu       sync.RWMutex
}

func NewInMemorySessionStore() session.Store {
	return &Store{sessions: make(map[string]*session_object.Session)}
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

	sess, err := session_object.NewSession(uuid.NewString(), ttl)
	if err != nil {
		return nil, err
	}

	store.sessions[sess.ID()] = sess
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
