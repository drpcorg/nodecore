package storages

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
)

type RedisStorage struct {
	Redis *redis.Client
	name  string
}

func (r *RedisStorage) storage() string {
	return "redis"
}

func NewRedisStorage(name string, redisConfig *config.RedisStorageConfig) (*RedisStorage, error) {
	options := &redis.Options{}
	var err error
	if redisConfig.FullUrl != "" {
		options, err = redis.ParseURL(redisConfig.FullUrl)
		if err != nil {
			return nil, fmt.Errorf("couldn't read the full redis url %s of storage '%s': %w", redisConfig.FullUrl, name, err)
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
	return &RedisStorage{
		Redis: client,
		name:  name,
	}, nil
}
