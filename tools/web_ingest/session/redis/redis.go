package redis_session

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blevesearch/bleve"
	"github.com/google/uuid"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/models"
	"github.com/mohammad-safakhou/newser/tools/web_ingest/session"
	"github.com/redis/go-redis/v9"
	"time"
)

type Store struct {
	client *redis.Client
}

func NewRedisSessionStore(addr, password string, db int) session.Store {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &Store{client: rdb}
}

func (store *Store) EnsureSession(id string, ttl time.Duration) (session.Session, error) {
	ctx := context.Background()
	if id != "" {
		key := fmt.Sprintf("session:%s:meta", id)
		exists, err := store.client.Exists(ctx, key).Result()
		if err == nil && exists == 1 {
			sess := &Session{client: store.client, id: id, expiresAt: time.Now().Add(ttl)}
			_ = store.client.Expire(ctx, key, ttl).Err()
			return sess, nil
		}
	}
	newID := uuid.NewString()
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	sess := &Session{
		client:    store.client,
		id:        newID,
		expiresAt: time.Now().Add(ttl),
		bleve:     index,
	}
	// Store empty meta to initialize
	metaKey := fmt.Sprintf("session:%s:meta", newID)
	_ = store.client.Set(ctx, metaKey, "{}", ttl).Err()
	return sess, nil
}

func (store *Store) GetSession(id string) (session.Session, error) {
	ctx := context.Background()
	key := fmt.Sprintf("session:%s:meta", id)
	exists, err := store.client.Exists(ctx, key).Result()
	if err != nil || exists == 0 {
		return nil, nil
	}
	return &Session{client: store.client, id: id}, nil
}

type Session struct {
	client    *redis.Client
	id        string
	expiresAt time.Time
	bleve     bleve.Index
}

func (s *Session) ID() string               { return s.id }
func (s *Session) Expire(ttl time.Duration) { s.expiresAt = time.Now().Add(ttl) }

func (s *Session) AddChunk(chunk models.DocChunk) error {
	ctx := context.Background()
	metaKey := fmt.Sprintf("session:%s:meta", s.id)
	// Get current meta
	val, err := s.client.Get(ctx, metaKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	meta := map[string]models.DocChunk{}
	if val != "" {
		_ = json.Unmarshal([]byte(val), &meta)
	}
	meta[chunk.DocID] = chunk
	data, _ := json.Marshal(meta)
	if err := s.client.Set(ctx, metaKey, data, time.Until(s.expiresAt)).Err(); err != nil {
		return err
	}
	// Bleve index is still in-memory per session
	if s.bleve == nil {
		index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
		if err != nil {
			return err
		}
		s.bleve = index
	}
	return s.bleve.Index(chunk.DocID, chunk)
}

func (s *Session) Chunks() []models.DocChunk {
	ctx := context.Background()
	metaKey := fmt.Sprintf("session:%s:meta", s.id)
	val, err := s.client.Get(ctx, metaKey).Result()
	if err != nil {
		return nil
	}
	meta := map[string]models.DocChunk{}
	_ = json.Unmarshal([]byte(val), &meta)
	out := make([]models.DocChunk, 0, len(meta))
	for _, c := range meta {
		out = append(out, c)
	}
	return out
}
