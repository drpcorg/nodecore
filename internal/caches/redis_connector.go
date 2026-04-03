package caches

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

func (r *RedisConnector) OutboxStore(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
) error {
	now := time.Now().UnixNano()

	pipe := r.client.TxPipeline()

	pipe.HSet(ctx, "outbox:data", key, value)
	pipe.ZAdd(ctx, "outbox:index", redis.Z{
		Score:  float64(now),
		Member: key,
	})

	if ttl > 0 {
		pipe.Set(ctx, "outbox:ttl:"+key, 1, ttl)
	}

	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisConnector) OutboxRemove(
	ctx context.Context,
	key string,
) error {
	pipe := r.client.TxPipeline()

	pipe.HDel(ctx, "outbox:data", key)
	pipe.ZRem(ctx, "outbox:index", key)
	pipe.Del(ctx, "outbox:ttl:"+key)

	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisConnector) OutboxList(
	ctx context.Context,
	cursor int64,
	limit int64,
) ([]outboxItem, error) {
	// cursor = timestamp
	minimum := strconv.FormatInt(cursor+1, 10)

	keys, err := r.client.ZRevRangeByScore(ctx, "outbox:index", &redis.ZRangeBy{
		Max:   "-inf",
		Min:   minimum,
		Count: limit,
	}).Result()
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return []outboxItem{}, nil
	}

	values, err := r.client.HMGet(ctx, "outbox:data", keys...).Result()
	if err != nil {
		return nil, err
	}

	result := make([]outboxItem, 0, len(keys))

	for i, key := range keys {
		if values[i] == nil {
			continue
		}

		result = append(result, outboxItem{
			key: values[i].([]byte),
		})
	}

	return result, nil
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
