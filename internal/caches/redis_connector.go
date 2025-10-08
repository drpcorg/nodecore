package caches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
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

func NewRedisConnector(id string, redisConfig *config.RedisCacheConnectorConfig) (*RedisConnector, error) {
	options := &redis.Options{}
	var err error
	if redisConfig.FullUrl != "" {
		options, err = redis.ParseURL(redisConfig.FullUrl)
		if err != nil {
			return nil, fmt.Errorf("couldn't read the full redis url %s of connector '%s': %w", redisConfig.FullUrl, id, err)
		}
	}

	if redisConfig.Address != "" {
		options.Addr = redisConfig.Address
	}
	if redisConfig.Username != "" {
		options.Username = redisConfig.Username
	}
	if redisConfig.Password != "" {
		options.Password = redisConfig.Password
	}
	if redisConfig.DB != nil {
		options.DB = *redisConfig.DB
	}

	if redisConfig.Timeouts != nil {
		if redisConfig.Timeouts.ConnectTimeout != nil && options.DialTimeout == 0 {
			options.DialTimeout = *redisConfig.Timeouts.ConnectTimeout
		}
		if redisConfig.Timeouts.ReadTimeout != nil && options.ReadTimeout == 0 {
			options.ReadTimeout = lo.Ternary(*redisConfig.Timeouts.ReadTimeout == 0, -1, *redisConfig.Timeouts.ReadTimeout)
		}
		if redisConfig.Timeouts.WriteTimeout != nil && options.WriteTimeout == 0 {
			options.WriteTimeout = lo.Ternary(*redisConfig.Timeouts.WriteTimeout == 0, -1, *redisConfig.Timeouts.WriteTimeout)
		}
	}

	if redisConfig.Pool != nil {
		if options.PoolSize == 0 {
			options.PoolSize = redisConfig.Pool.Size
		}
		if redisConfig.Pool.PoolTimeout != nil && options.PoolTimeout == 0 {
			options.PoolTimeout = *redisConfig.Pool.PoolTimeout
		}
		if options.MinIdleConns == 0 {
			options.MinIdleConns = redisConfig.Pool.MinIdleConns
		}
		if options.MaxIdleConns == 0 {
			options.MaxIdleConns = redisConfig.Pool.MaxIdleConns
		}
		if options.MaxActiveConns == 0 {
			options.MaxActiveConns = redisConfig.Pool.MaxActiveConns
		}
		if redisConfig.Pool.ConnMaxIdleTime != nil && options.ConnMaxIdleTime == 0 {
			options.ConnMaxIdleTime = *redisConfig.Pool.ConnMaxIdleTime
		}
		if redisConfig.Pool.ConnMaxLifeTime != nil && options.ConnMaxLifetime == 0 {
			options.ConnMaxLifetime = *redisConfig.Pool.ConnMaxLifeTime
		}
	}

	client := redis.NewClient(options)

	return &RedisConnector{
		id:     id,
		client: client,
	}, nil
}

var _ CacheConnector = (*RedisConnector)(nil)
