package redis_repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-redis/redis/v8"
	"github.com/mohammad-safakhou/newser/models"
)

const topicKeyPrefix = "topic:"

// redisTopicRepository implements TopicRepository using Redis
type redisTopicRepository struct {
	client *redis.Client
}

func (r redisTopicRepository) SaveTopic(ctx context.Context, topic models.Topic) error {
	key := topicKeyPrefix + topic.Title

	// Marshal the topic struct to JSON before storing
	data, err := json.Marshal(topic)
	if err != nil {
		return err
	}

	// Store the topic in Redis
	err = r.client.Set(ctx, key, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func (r redisTopicRepository) GetAllTopics(ctx context.Context) ([]models.Topic, error) {
	keys, err := r.client.Keys(ctx, topicKeyPrefix+"*").Result()
	if err != nil {
		return nil, err
	}

	var topics []models.Topic
	for _, key := range keys {
		val, err := r.client.Get(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		var topic models.Topic
		if err := json.Unmarshal([]byte(val), &topic); err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}

	return topics, nil
}

func (r redisTopicRepository) GetTopic(ctx context.Context, title string) (models.Topic, error) {
	key := topicKeyPrefix + title

	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return models.Topic{}, models.ErrTopicNotFound
		}
		return models.Topic{}, err
	}

	var topic models.Topic
	if err := json.Unmarshal([]byte(val), &topic); err != nil {
		return models.Topic{}, err
	}

	return topic, nil
}

func (r redisTopicRepository) DeleteTopic(ctx context.Context, title string) error {
	key := topicKeyPrefix + title

	// Delete the topic from Redis
	err := r.client.Del(ctx, key).Err()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return models.ErrTopicNotFound
		}
		return err
	}

	return nil
}

func NewRedisTopicRepository(client *redis.Client) *redisTopicRepository {
	return &redisTopicRepository{
		client: client,
	}
}
