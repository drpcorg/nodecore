package ratelimiter_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestRedisEngine_Execute_AllowsRequestsUnderLimit(t *testing.T) {
	db, mock := redismock.NewClientMock()
	engine := ratelimiter.NewRateLimitRedisEngine("test", db)

	mock.ExpectIncr("ratelimit:test:budget1").SetVal(1)
	mock.ExpectExpire("ratelimit:test:budget1", 1*time.Minute).SetVal(true)
	mock.ExpectIncr("ratelimit:test:budget2").SetVal(2)
	mock.ExpectExpire("ratelimit:test:budget2", 1*time.Minute).SetVal(true)

	commands := []ratelimiter.RateLimitCommand{
		{
			Name: "budget1",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
		{
			Name: "budget2",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
	}

	allowed, err := engine.Execute(commands)

	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisEngine_Execute_DeniesRequestsOverLimit(t *testing.T) {
	db, mock := redismock.NewClientMock()
	engine := ratelimiter.NewRateLimitRedisEngine("test", db)

	mock.ExpectIncr("ratelimit:test:budget1").SetVal(11)
	mock.ExpectExpire("ratelimit:test:budget1", 1*time.Minute).SetVal(true)

	commands := []ratelimiter.RateLimitCommand{
		{
			Name: "budget1",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
	}

	allowed, err := engine.Execute(commands)

	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisEngine_Execute_DeniesIfAnyBudgetExceeded(t *testing.T) {
	db, mock := redismock.NewClientMock()
	engine := ratelimiter.NewRateLimitRedisEngine("test", db)

	mock.ExpectIncr("ratelimit:test:budget1").SetVal(5)
	mock.ExpectExpire("ratelimit:test:budget1", 1*time.Minute).SetVal(true)
	mock.ExpectIncr("ratelimit:test:budget2").SetVal(15)
	mock.ExpectExpire("ratelimit:test:budget2", 1*time.Minute).SetVal(true)

	commands := []ratelimiter.RateLimitCommand{
		{
			Name: "budget1",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
		{
			Name: "budget2",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
	}

	allowed, err := engine.Execute(commands)

	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisEngine_Execute_HandlesRedisError(t *testing.T) {
	db, mock := redismock.NewClientMock()
	engine := ratelimiter.NewRateLimitRedisEngine("test", db)

	mock.ExpectIncr("ratelimit:test:budget1").SetErr(redis.ErrClosed)

	commands := []ratelimiter.RateLimitCommand{
		{
			Name: "budget1",
			Type: ratelimiter.NewFixedRateLimiterType(10, 1*time.Minute),
		},
	}

	allowed, err := engine.Execute(commands)

	assert.Error(t, err)
	assert.False(t, allowed)
}

func TestRedisEngine_Execute_EmptyCommands(t *testing.T) {
	db, _ := redismock.NewClientMock()
	engine := ratelimiter.NewRateLimitRedisEngine("test", db)

	commands := []ratelimiter.RateLimitCommand{}

	allowed, err := engine.Execute(commands)

	assert.NoError(t, err)
	assert.True(t, allowed)
}
