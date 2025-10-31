package ratelimiter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitMemoryEngine_Execute_SingleCommand_AllowsRequests(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	// Create a rate limiter that allows 5 requests per second
	cmd := RateLimitCommand{
		Name: "test-limiter",
		Type: &FixedRateLimiterType{
			requests: 5,
			period:   time.Second,
		},
	}

	// First request should be allowed
	result, err := engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)

}

func TestRateLimitMemoryEngine_Execute_MultipleCommands(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	// Create two different rate limiters
	commands := []RateLimitCommand{
		{
			Name: "limiter-1",
			Type: &FixedRateLimiterType{
				requests: 10,
				period:   time.Second,
			},
		},
		{
			Name: "limiter-2",
			Type: &FixedRateLimiterType{
				requests: 5,
				period:   time.Second,
			},
		},
	}

	// Execute both commands
	result, err := engine.Execute(commands)
	require.NoError(t, err)
	assert.True(t, result)
}

func TestRateLimitMemoryEngine_Execute_RateLimitExceeded(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	// Create a rate limiter that allows only 2 requests
	cmd := RateLimitCommand{
		Name: "strict-limiter",
		Type: &FixedRateLimiterType{
			requests: 2,
			period:   time.Second,
		},
	}

	// First two requests should succeed
	result, err := engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)

	result, err = engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)

	// Third request should be rate limited
	result, err = engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.False(t, result)
}

func TestRateLimitMemoryEngine_Execute_ReusesExistingLimiter(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	cmd := RateLimitCommand{
		Name: "reused-limiter",
		Type: &FixedRateLimiterType{
			requests: 3,
			period:   time.Second,
		},
	}

	// First execution creates the limiter
	result, err := engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)

	// Second execution reuses the same limiter
	result, err = engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)
}

func TestRateLimitMemoryEngine_Execute_EmptyCommands(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	// Execute with empty command list
	result, err := engine.Execute([]RateLimitCommand{})
	require.NoError(t, err)
	assert.True(t, result, "Empty command list should return true")
}

func TestRateLimitMemoryEngine_Execute_RateLimitRecovery(t *testing.T) {
	engine := NewRateLimitMemoryEngine()

	// Create a rate limiter with very short period for testing
	cmd := RateLimitCommand{
		Name: "recovery-limiter",
		Type: &FixedRateLimiterType{
			requests: 1,
			period:   50 * time.Millisecond,
		},
	}

	// First request should succeed
	result, err := engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)

	// Second request should fail
	result, err = engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.False(t, result)

	// Wait for the period to pass
	time.Sleep(60 * time.Millisecond)

	// After recovery period, request should succeed again
	result, err = engine.Execute([]RateLimitCommand{cmd})
	require.NoError(t, err)
	assert.True(t, result)
}
