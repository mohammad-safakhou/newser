package session

import (
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"time"
)

// Store interface for session management
type Store interface {
	EnsureSession(id string, ttl time.Duration) (Session, error)
	GetSession(id string) (Session, error)
}

// Session interface for session operations
type Session interface {
	ID() string
	Expire(ttl time.Duration)
	AddChunk(chunk models.DocChunk) error
	Chunks() []models.DocChunk
}
