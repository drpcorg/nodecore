package ratelimiter

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/rs/zerolog"
)

type UpstreamAutoTune struct {
	period             time.Duration
	rateLimitPeriod    time.Duration
	ratelimit          atomic.Int32
	errorRateThreshold float64
	bucket             atomic.Pointer[ratelimit.Bucket]
	accumErrors        atomic.Int32
	accumRateLimited   atomic.Int32
	attemptCounts      atomic.Pointer[utils.CMap[int64, atomic.Int32]]
}

func NewUpstreamAutoTune(ctx context.Context, config *config.RateLimitAutoTuneConfig) *UpstreamAutoTune {
	result := &UpstreamAutoTune{
		period:             config.Period,
		rateLimitPeriod:    config.InitRateLimitPeriod,
		errorRateThreshold: config.ErrorRateThreshold,
		bucket:             atomic.Pointer[ratelimit.Bucket]{},
		attemptCounts:      atomic.Pointer[utils.CMap[int64, atomic.Int32]]{},
	}
	result.ratelimit.Store(int32(config.InitRateLimit))
	result.bucket.Store(ratelimit.NewBucketWithQuantum(config.InitRateLimitPeriod, int64(config.InitRateLimit), int64(config.InitRateLimit)))
	result.attemptCounts.Store(utils.NewCMap[int64, atomic.Int32]())
	go func() {
		result.Run(ctx)
	}()
	return result
}

func (u *UpstreamAutoTune) Allow() bool {
	// register in bucket how much requests were made in current period
	timeKey := time.Now().UnixMilli() / int64(u.rateLimitPeriod)
	result, _ := u.attemptCounts.Load().LoadOrStore(timeKey, &atomic.Int32{})
	result.Add(1)

	allowed := u.bucket.Load().TakeAvailable(1) > 0
	if !allowed {
		u.accumRateLimited.Add(1)
	}
	return allowed
}

func (u *UpstreamAutoTune) Run(ctx context.Context) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(u.period):
			u.RecalculateRateLimit(ctx)
		}
	}
}

func (u *UpstreamAutoTune) RecalculateRateLimit(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	errors := u.accumErrors.Swap(0)
	rateLimited := u.accumRateLimited.Swap(0)
	oldMap := u.attemptCounts.Swap(utils.NewCMap[int64, atomic.Int32]())

	totalAttempts := 0
	oldMap.Range(func(key int64, val *atomic.Int32) bool {
		totalAttempts += int(val.Load())
		return true
	})

	oldLimit := int(u.ratelimit.Load())
	newLimit := oldLimit
	rateLimitedRate := float64(rateLimited) / float64(totalAttempts)

	peakRequests := 0
	oldMap.Range(func(key int64, val *atomic.Int32) bool {
		if int(val.Load()) > peakRequests {
			peakRequests = int(val.Load())
		}
		return true
	})

	peakUtilization := float64(peakRequests) / float64(oldLimit)
	allowed := totalAttempts - int(rateLimited)
	errorRate := 0.0
	if allowed > 0 {
		errorRate = float64(errors) / float64(allowed)
	}

	if errorRate >= u.errorRateThreshold && allowed > 0 {
		reductionFactor := 0.7
		if errorRate >= u.errorRateThreshold*2 {
			reductionFactor = 0.5
		}
		newLimit = int(math.Ceil(float64(oldLimit) * reductionFactor))
		log.Info().
			Int("old_limit", oldLimit).
			Int("new_limit", newLimit).
			Int32("errors", errors).
			Int32("rate_limited", rateLimited).
			Int("total_attempts", totalAttempts).
			Float64("error_rate", errorRate).
			Int("allowed", allowed).
			Float64("threshold", u.errorRateThreshold).
			Msg("auto-tune: reducing rate limit due to errors")
	} else if errors == 0 && (peakUtilization > 0.95 || rateLimitedRate > u.errorRateThreshold) {
		newLimit = int(math.Ceil(float64(oldLimit) * 1.1))
		log.Info().
			Int("old_limit", oldLimit).
			Int("new_limit", newLimit).
			Int("peak_requests", peakRequests).
			Float64("peak_utilization", peakUtilization).
			Float64("rate_limited_rate", rateLimitedRate).
			Int("total_attempts", totalAttempts).
			Int("allowed", allowed).
			Msg("auto-tune: increasing rate limit")
	}

	if newLimit < 1 {
		newLimit = 1
	}

	if newLimit != oldLimit {
		u.ratelimit.Store(int32(newLimit))
		u.bucket.Store(ratelimit.NewBucketWithQuantum(u.rateLimitPeriod, int64(newLimit), int64(newLimit)))
	}
}

func (u *UpstreamAutoTune) IncErrors() {
	u.accumErrors.Add(1)
}
