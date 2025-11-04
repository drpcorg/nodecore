package ratelimiter

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type RateLimitRedisEngine struct {
	name  string
	redis *redis.Client
}

func NewRateLimitRedisEngine(name string, redis *redis.Client) *RateLimitRedisEngine {
	return &RateLimitRedisEngine{
		name:  name,
		redis: redis,
	}
}

func (e *RateLimitRedisEngine) Execute(cmd []RateLimitCommand) (bool, error) {
	ctx := context.Background()

	pipe := e.redis.Pipeline()
	cmds := make([]*redis.IntCmd, 0, len(cmd))

	for _, c := range cmd {
		tp, ok := c.Type.(*FixedRateLimiterType)
		if !ok {
			continue
		}

		redisKey := "ratelimit:" + e.name + ":" + c.Name
		incrCmd := pipe.Incr(ctx, redisKey)
		cmds = append(cmds, incrCmd)
		pipe.Expire(ctx, redisKey, tp.period)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	result := true
	for i, c := range cmd {
		tp, ok := c.Type.(*FixedRateLimiterType)
		if !ok {
			continue
		}

		count := cmds[i].Val()
		if int(count) > tp.requests {
			result = false
		}
	}

	return result, nil
}
