package ratelimiter

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestUpstreamAutoTune_Allow_BasicRateLimiting(t *testing.T) {
	ctx := context.Background()
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       5,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Should allow up to InitRateLimit requests
	for i := 0; i < 5; i++ {
		allowed := autotune.Allow()
		assert.True(t, allowed, "Request %d should be allowed", i+1)
	}

	// Next request should be rate limited
	allowed := autotune.Allow()
	assert.False(t, allowed, "Request beyond limit should be denied")
}

func TestUpstreamAutoTune_RecalculateRateLimit_ReduceLimitOnHighErrors(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       100,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Simulate requests
	for i := 0; i < 50; i++ {
		autotune.Allow()
	}

	// Simulate errors above threshold (15% error rate)
	for i := 0; i < 8; i++ {
		autotune.IncErrors()
	}

	oldLimit := int(autotune.ratelimit.Load())

	// Trigger recalculation
	autotune.RecalculateRateLimit(ctx)

	newLimit := int(autotune.ratelimit.Load())

	assert.Less(t, newLimit, oldLimit, "Rate limit should be reduced")
}

func TestUpstreamAutoTune_RecalculateRateLimit_IncreaseLimitOnHighUtilization(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              50 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       10,
		InitRateLimitPeriod: 50 * time.Millisecond,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Simulate high utilization without errors
	// Make requests at peak near the limit
	for i := 0; i < 9; i++ {
		autotune.Allow()
	}

	// Wait a bit and make more requests in next period
	time.Sleep(60 * time.Millisecond)
	for i := 0; i < 10; i++ {
		autotune.Allow()
	}

	oldLimit := int(autotune.ratelimit.Load())

	autotune.RecalculateRateLimit(ctx)
	newLimit := int(autotune.ratelimit.Load())
	assert.Greater(t, newLimit, oldLimit, "Rate limit should be increased")
}

func TestUpstreamAutoTune_RecalculateRateLimit_IncreaseLimitOnHighRateLimited(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       10,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Simulate many requests, some will be rate limited
	for i := 0; i < 30; i++ {
		autotune.Allow()
	}

	oldLimit := int(autotune.ratelimit.Load())

	// Trigger recalculation (no errors, high rate-limited ratio)
	autotune.RecalculateRateLimit(ctx)

	newLimit := int(autotune.ratelimit.Load())
	assert.Greater(t, newLimit, oldLimit, "Rate limit should be increased")
}

func TestUpstreamAutoTune_RecalculateRateLimit_NoChangeOnStableLoad(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       100,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)
	for i := 0; i < 50; i++ {
		autotune.Allow()
	}

	oldLimit := int(autotune.ratelimit.Load())

	autotune.RecalculateRateLimit(ctx)

	newLimit := int(autotune.ratelimit.Load())
	assert.Equal(t, oldLimit, newLimit, "Rate limit should remain stable")
}

func TestUpstreamAutoTune_RecalculateRateLimit_NoRequestsNoChange(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       100,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	oldLimit := int(autotune.ratelimit.Load())

	autotune.RecalculateRateLimit(ctx)

	newLimit := int(autotune.ratelimit.Load())

	assert.Equal(t, oldLimit, newLimit, "Rate limit should not change with no requests")
}

func TestUpstreamAutoTune_RecalculateRateLimit_ErrorsButNoAllowedRequests(t *testing.T) {
	ctx := zerolog.New(zerolog.NewTestWriter(t)).WithContext(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       10,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Try to make many requests (most will be denied)
	for i := 0; i < 30; i++ {
		autotune.Allow()
	}

	// Add errors
	autotune.IncErrors()
	autotune.IncErrors()

	// Trigger recalculation
	autotune.RecalculateRateLimit(ctx)

	newLimit := int(autotune.ratelimit.Load())

	// Error rate calculation should handle the case properly
	// Since allowed = totalAttempts - rateLimited
	// Error rate = errors / allowed
	assert.NotEqual(t, 0, newLimit, "Rate limit should not be zero")
	assert.GreaterOrEqual(t, newLimit, 1, "Rate limit should be at least 1")
}

func TestUpstreamAutoTune_Run_CancelsOnContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              1 * time.Second,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       100,
		InitRateLimitPeriod: time.Second,
	}

	autotune := &UpstreamAutoTune{
		period:             cfg.Period,
		rateLimitPeriod:    cfg.InitRateLimitPeriod,
		errorRateThreshold: cfg.ErrorRateThreshold,
	}
	autotune.ratelimit.Store(int32(cfg.InitRateLimit))

	done := make(chan bool)
	go func() {
		autotune.Run(ctx)
		done <- true
	}()

	// Cancel the context
	cancel()

	// Wait for Run to finish
	select {
	case <-done:
		// Success - Run exited
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run did not exit after context cancellation")
	}
}

func TestUpstreamAutoTune_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	cfg := &config.RateLimitAutoTuneConfig{
		Period:              100 * time.Millisecond,
		ErrorRateThreshold:  0.1,
		InitRateLimit:       1000,
		InitRateLimitPeriod: time.Second,
	}

	autotune := NewUpstreamAutoTune(ctx, cfg)

	// Run concurrent operations
	done := make(chan bool)

	// Concurrent Allow calls
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				autotune.Allow()
			}
			done <- true
		}()
	}

	// Concurrent IncErrors calls
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				autotune.IncErrors()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	// Should not panic and should have valid state
	limit := autotune.ratelimit.Load()
	assert.Greater(t, limit, int32(0))
}
