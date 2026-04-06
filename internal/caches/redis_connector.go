package caches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/redis/go-redis/v9"
)

const cacheKeyPrefix = "nodecore:entry:"

type RedisConnector struct {
	id     string
	client *redis.Client
}

func (r *RedisConnector) Initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := r.client.Ping(ctx).Err()
	if err != nil {
		return fmt.Errorf("could not connect to redis, %w", err)
	}

	return nil
}

func (r *RedisConnector) Id() string {
	return r.id
}

func (r *RedisConnector) Store(ctx context.Context, key string, object string, ttl time.Duration) error {
	cacheKey := cacheKeyPrefix + key

	return r.client.Set(ctx, cacheKey, object, ttl).Err()
}

func (r *RedisConnector) Receive(ctx context.Context, key string) ([]byte, error) {
	cacheKey := cacheKeyPrefix + key

	value, err := r.client.Get(ctx, cacheKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCacheNotFound
	}
	return value, err
}

func NewRedisConnector(id string, redisConfig *config.RedisCacheConnectorConfig, storageRegistry *storages.StorageRegistry) (*RedisConnector, error) {
	storage, ok := storageRegistry.Get(redisConfig.StorageName)
	if !ok {
		return nil, fmt.Errorf("redis storage with name %s not found", redisConfig.StorageName)
	}
	redisStorage, ok := storage.(*storages.RedisStorage)
	if !ok {
		return nil, fmt.Errorf("redis storage with name %s is not a redis storage", redisConfig.StorageName)
	}
	return &RedisConnector{
		id:     id,
		client: redisStorage.Redis,
	}, nil
}

var _ CacheConnector = (*RedisConnector)(nil)
