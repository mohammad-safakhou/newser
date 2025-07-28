package repository

import (
	"context"
	"fmt"
	"github.com/mohammad-safakhou/newser/config"
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
		c, err := redis_repository.Conn(
			ctx,
			config.AppConfig.Databases.Redis.Host,
			config.AppConfig.Databases.Redis.Port,
			config.AppConfig.Databases.Redis.Pass,
			config.AppConfig.Databases.Redis.DB,
			config.AppConfig.Databases.Redis.Timeout,
		)
		if err != nil {
			return nil, err
		}
		return redis_repository.NewRedisTopicRepository(c), nil
	}
	return nil, fmt.Errorf("invalid repository type: %s", t)
}
