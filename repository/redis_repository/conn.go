package redis_repository

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"log"
	"time"
)

func Conn(ctx context.Context, host, port, pass string, db int, timeout time.Duration) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", host, port),
		DialTimeout: timeout,
		Password:    pass,
		DB:          db,
	})
	log.Println("redis options -> " + client.String() + ", password: " + pass)

	pong, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	if pong != "PONG" {
		return nil, fmt.Errorf("expected PONG, got %s", pong)
	}

	return client, nil
}
