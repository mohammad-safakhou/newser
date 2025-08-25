package repository

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
	"github.com/mohammad-safakhou/newser/models"
	"github.com/mohammad-safakhou/newser/repository/redis_repository"
)

// TopicRepository defines the interface for topic storage
type TopicRepository interface {
	SaveTopic(ctx context.Context, topic models.Topic) error
	GetAllTopics(ctx context.Context) ([]models.Topic, error)
	GetTopic(ctx context.Context, title string) (models.Topic, error)
	DeleteTopic(ctx context.Context, title string) error
}

type RepoType string

const (
	RepoTypeRedis = "redis"
)

func NewTopicRepository(ctx context.Context, t RepoType) (TopicRepository, error) {
	switch t {
	case RepoTypeRedis:
		// Pull from env to reduce config drift
		host := env("REDIS_HOST", "localhost")
		port := env("REDIS_PORT", "6379")
		pass := os.Getenv("REDIS_PASSWORD")
		db := 0
		timeout := 5 * time.Second
		if v := os.Getenv("REDIS_DB"); v != "" { if n, err := strconv.Atoi(v); err == nil { db = n } }
		if v := os.Getenv("REDIS_TIMEOUT"); v != "" { if d, err := time.ParseDuration(v); err == nil { timeout = d } }
		c, err := redis_repository.Conn(ctx, host, port, pass, db, timeout)
		if err != nil {
			return nil, err
		}
		return redis_repository.NewRedisTopicRepository(c), nil
	}
	return nil, fmt.Errorf("invalid repository type: %s", t)
}

func env(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }
