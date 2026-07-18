package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type l2layer struct {
	Client *redis.Client
}

// L2aCache is a Redis-backed distributed cache.
type L2aCache struct {
	Client *redis.Client
}

func NewL2aCache(addr, password string, db int) *L2aCache {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	return &L2aCache{Client: rdb}
}

// Get retrieves a value from Redis for the normalized query.
func (c *L2aCache) Get(ctx context.Context, tenantID, query string) (string, error) {
	key := fmt.Sprintf("norm:%s:%s", tenantID, query)
	val, err := c.Client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil // cache miss
	}
	if err != nil {
		return "", fmt.Errorf("redis get error: %w", err)
	}
	return val, nil
}

// Set stores a value in Redis with the specified TTL.
func (c *L2aCache) Set(ctx context.Context, tenantID, query, value string, ttl time.Duration) error {
	key := fmt.Sprintf("norm:%s:%s", tenantID, query)
	err := c.Client.Set(ctx, key, value, ttl).Err()
	if err != nil {
		return fmt.Errorf("redis set error: %w", err)
	}
	return nil
}

// Close closes the underlying redis client.
func (c *L2aCache) Close() error {
	return c.Client.Close()
}
