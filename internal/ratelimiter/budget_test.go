package ratelimiter

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitBudget_Allow_BasicMethodLimit(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Method:   "eth_blockNumber",
					Requests: 2,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestRateLimitBudget_Allow_MethodNotMatchingRule(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Method:   "eth_blockNumber",
					Requests: 1,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	// Exhaust the limit for eth_blockNumber
	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.False(t, allowed)

	// Different method should not be affected
	allowed, err = budget.Allow("net_version")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimitBudget_Allow_PatternRule(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Pattern:  "eth_.*",
					Requests: 3,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	// All these methods match the pattern and share the same limit
	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_getBalance")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_call")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_sendTransaction")
	require.NoError(t, err)
	assert.False(t, allowed)

	// Non-matching method should be allowed
	allowed, err = budget.Allow("net_version")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimitBudget_Allow_MultipleRulesStricterLimitApplies(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Method:   "eth_blockNumber",
					Requests: 5,
					Period:   time.Second,
				},
				{
					Pattern:  "eth_.*",
					Requests: 2,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	// eth_blockNumber matches both rules
	// The pattern rule (2 requests) is stricter
	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestRateLimitBudget_Allow_SeparateLimitsPerMethod(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Method:   "eth_blockNumber",
					Requests: 2,
					Period:   time.Second,
				},
				{
					Method:   "eth_getBalance",
					Requests: 2,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	// Exhaust eth_blockNumber limit
	budget.Allow("eth_blockNumber")
	budget.Allow("eth_blockNumber")

	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.False(t, allowed)

	// eth_getBalance should have its own independent limit
	allowed, err = budget.Allow("eth_getBalance")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_getBalance")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_getBalance")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestRateLimitBudget_Allow_ComplexPattern(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Pattern:  "^eth_(get|call).*",
					Requests: 3,
					Period:   time.Second,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	// Methods matching the pattern
	allowed, err := budget.Allow("eth_getBalance")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_call")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_getBlockByNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_callMany")
	require.NoError(t, err)
	assert.False(t, allowed)

	// Methods not matching the pattern should be allowed
	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_sendRawTransaction")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimitBudget_Allow_RateLimitRecovery(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{
				{
					Method:   "eth_blockNumber",
					Requests: 1,
					Period:   50 * time.Millisecond,
				},
			},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	allowed, err := budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.False(t, allowed)

	time.Sleep(60 * time.Millisecond)

	allowed, err = budget.Allow("eth_blockNumber")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimitBudget_Allow_EmptyRules(t *testing.T) {
	cfg := &config.RateLimitBudget{
		Name: "test-budget",
		Config: &config.RateLimiterConfig{
			Rules: []config.RateLimitRule{},
		},
	}

	budget := NewRateLimitBudget(cfg, NewRateLimitMemoryEngine())

	for i := 0; i < 10; i++ {
		allowed, err := budget.Allow("any_method")
		require.NoError(t, err)
		assert.True(t, allowed)
	}
}
