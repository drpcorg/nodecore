package outbox

import (
	"context"
	"fmt"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/redis/go-redis/v9"
	"strconv"
	"time"
)

const (
	outboxKeyDataPrefix  = "nodecore:outbox:data"
	outboxKeyIndexPrefix = "nodecore:outbox:index"
	outboxKeyTTLPrefix   = "nodecore:outbox:ttl"
)

type redisClient struct {
	client *redis.Client
}

func newRedisClient(storage *storages.RedisStorage) (*redisClient, error) {
	r := &redisClient{
		client: storage.Redis,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := r.client.Ping(ctx).Err()
	if err != nil {
		return nil, fmt.Errorf("could not connect to redis: %w", err)
	}
	return r, nil
}

func (r *redisClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	now := time.Now().UnixNano()

	pipe := r.client.TxPipeline()

	pipe.HSet(ctx, outboxKeyDataPrefix, key, value)
	pipe.ZAdd(ctx, outboxKeyIndexPrefix, redis.Z{
		Score:  float64(now),
		Member: key,
	})

	if ttl > 0 {
		pipe.Set(ctx, outboxKeyTTLPrefix+":"+key, 1, ttl)
	}

	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisClient) Delete(ctx context.Context, key string) error {
	pipe := r.client.TxPipeline()

	pipe.HDel(ctx, outboxKeyDataPrefix, key)
	pipe.ZRem(ctx, outboxKeyIndexPrefix, key)
	pipe.Del(ctx, outboxKeyTTLPrefix+":"+key)

	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisClient) List(ctx context.Context, cursor int64, limit int64) ([]outboxItem, error) {
	// cursor = timestamp
	minimum := strconv.FormatInt(cursor+1, 10)

	keys, err := r.client.ZRevRangeByScore(ctx, outboxKeyIndexPrefix, &redis.ZRangeBy{
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

	values, err := r.client.HMGet(ctx, outboxKeyDataPrefix, keys...).Result()
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
