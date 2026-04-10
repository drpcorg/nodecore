package outbox

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	outboxKeyDataPrefix  = "nodecore:outbox:data"
	outboxKeyIndexPrefix = "nodecore:outbox:index"
	outboxKeyTTLPrefix   = "nodecore:outbox:ttl"

	expiredCleanupBatchSize = 256
)

type Memorizer interface {
	Ping(ctx context.Context) *redis.StatusCmd
	TxPipeline() redis.Pipeliner
	ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd
	HMGet(ctx context.Context, key string, fields ...string) *redis.SliceCmd
}

type redisClient struct {
	client Memorizer
}

func newRedisClient(redis Memorizer) (*redisClient, error) {
	redisClientInstance := &redisClient{
		client: redis,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := redisClientInstance.client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("could not connect to redis: %w", err)
	}

	return redisClientInstance, nil
}

func (r *redisClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	createdAtUnixNano := time.Now().UnixNano()

	pipeline := r.client.TxPipeline()

	pipeline.HSet(ctx, outboxKeyDataPrefix, key, value)
	pipeline.ZAdd(ctx, outboxKeyIndexPrefix, redis.Z{
		Score:  float64(createdAtUnixNano),
		Member: key,
	})

	if ttl > 0 {
		expiresAtUnixNano := time.Now().Add(ttl).UnixNano()
		pipeline.ZAdd(ctx, outboxKeyTTLPrefix, redis.Z{
			Score:  float64(expiresAtUnixNano),
			Member: key,
		})
	} else {
		pipeline.ZRem(ctx, outboxKeyTTLPrefix, key)
	}

	if _, err := pipeline.Exec(ctx); err != nil {
		return fmt.Errorf("set outbox item: %w", err)
	}

	return nil
}

func (r *redisClient) Delete(ctx context.Context, key string) error {
	pipeline := r.client.TxPipeline()

	pipeline.HDel(ctx, outboxKeyDataPrefix, key)
	pipeline.ZRem(ctx, outboxKeyIndexPrefix, key)
	pipeline.ZRem(ctx, outboxKeyTTLPrefix, key)

	if _, err := pipeline.Exec(ctx); err != nil {
		return fmt.Errorf("delete outbox item: %w", err)
	}

	return nil
}

func (r *redisClient) List(ctx context.Context, cursor int64, limit int64) ([]Item, error) {
	if limit <= 0 {
		return []Item{}, nil
	}

	if err := r.cleanupExpired(ctx); err != nil {
		return nil, fmt.Errorf("cleanup expired outbox items: %w", err)
	}

	minimumCreatedAt := strconv.FormatInt(cursor+1, 10)

	keys, err := r.client.ZRangeByScore(ctx, outboxKeyIndexPrefix, &redis.ZRangeBy{
		Min:   minimumCreatedAt,
		Max:   "+inf",
		Count: limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("read outbox index: %w", err)
	}

	if len(keys) == 0 {
		return []Item{}, nil
	}

	values, err := r.client.HMGet(ctx, outboxKeyDataPrefix, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read outbox data: %w", err)
	}

	result := make([]Item, 0, len(keys))
	orphanedKeys := make([]string, 0)

	for index, key := range keys {
		rawValue := values[index]
		if rawValue == nil {
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		var valueBytes []byte

		switch typedValue := rawValue.(type) {
		case string:
			valueBytes = []byte(typedValue)
		case []byte:
			valueBytes = typedValue
		default:
			return nil, fmt.Errorf("unexpected redis hash value type %T for key %q", rawValue, key)
		}

		result = append(result, Item{
			key, valueBytes,
		})
	}

	if len(orphanedKeys) > 0 {
		if err := r.removeKeys(ctx, orphanedKeys); err != nil {
			return nil, fmt.Errorf("cleanup orphaned outbox items: %w", err)
		}
	}

	return result, nil
}

func (r *redisClient) cleanupExpired(ctx context.Context) error {
	nowUnixNano := time.Now().UnixNano()
	maximumExpiration := strconv.FormatInt(nowUnixNano, 10)

	for {
		expiredKeys, err := r.client.ZRangeByScore(ctx, outboxKeyTTLPrefix, &redis.ZRangeBy{
			Min:   "-inf",
			Max:   maximumExpiration,
			Count: expiredCleanupBatchSize,
		}).Result()
		if err != nil {
			return fmt.Errorf("read expired outbox keys: %w", err)
		}

		if len(expiredKeys) == 0 {
			return nil
		}

		if err := r.removeKeys(ctx, expiredKeys); err != nil {
			return err
		}

		if len(expiredKeys) < expiredCleanupBatchSize {
			return nil
		}
	}
}

func (r *redisClient) removeKeys(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	pipeline := r.client.TxPipeline()

	hashFields := make([]string, len(keys))
	zsetMembers := make([]interface{}, len(keys))

	for index, key := range keys {
		hashFields[index] = key
		zsetMembers[index] = key
	}

	pipeline.HDel(ctx, outboxKeyDataPrefix, hashFields...)
	pipeline.ZRem(ctx, outboxKeyIndexPrefix, zsetMembers...)
	pipeline.ZRem(ctx, outboxKeyTTLPrefix, zsetMembers...)

	if _, err := pipeline.Exec(ctx); err != nil {
		return fmt.Errorf("remove outbox keys: %w", err)
	}

	return nil
}
