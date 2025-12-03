package ratelimiter

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

var TunedRateLimitMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "ratelimiter_auto_tune",
		Name:      "tuned_rate_limit",
		Help:      "The current auto-tuned rate limit for an upstream",
	},
	[]string{"upstream", "period"},
)

type direction int

const (
	directionStable direction = iota
	directionIncrease
	directionDecrease
)

type UpstreamAutoTune struct {
	period             time.Duration
	rateLimitPeriod    time.Duration
	upstreamId         string
	ratelimit          atomic.Int32
	errorRateThreshold float64
	bucket             atomic.Pointer[ratelimit.Bucket]
	accumErrors        atomic.Int32
	accumRateLimited   atomic.Int32
	attemptCounts      atomic.Pointer[utils.CMap[int64, *atomic.Int32]]
	lastDirection      direction
	stepPercent        float64
}

func NewUpstreamAutoTune(ctx context.Context, upstreamId string, config *config.RateLimitAutoTuneConfig) *UpstreamAutoTune {
	result := &UpstreamAutoTune{
		period:             config.Period,
		upstreamId:         upstreamId,
		rateLimitPeriod:    config.InitRateLimitPeriod,
		errorRateThreshold: config.ErrorRateThreshold,
		bucket:             atomic.Pointer[ratelimit.Bucket]{},
		attemptCounts:      atomic.Pointer[utils.CMap[int64, *atomic.Int32]]{},
		stepPercent:        10.0,
		lastDirection:      directionStable,
	}
	result.ratelimit.Store(int32(config.InitRateLimit))
	result.bucket.Store(ratelimit.NewBucketWithQuantum(config.InitRateLimitPeriod, int64(config.InitRateLimit), int64(config.InitRateLimit)))
	result.attemptCounts.Store(utils.NewCMap[int64, *atomic.Int32]())
	TunedRateLimitMetric.WithLabelValues(upstreamId, config.InitRateLimitPeriod.String()).Set(float64(config.InitRateLimit))
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
	oldMap := u.attemptCounts.Swap(utils.NewCMap[int64, *atomic.Int32]())

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

	var newDirection direction

	if errorRate >= u.errorRateThreshold && allowed > 0 {
		newDirection = directionDecrease
		newLimit = u.applyAdaptiveStep(oldLimit, newDirection)

		log.Info().
			Int("old_limit", oldLimit).
			Int("new_limit", newLimit).
			Int32("errors", errors).
			Int32("rate_limited", rateLimited).
			Int("total_attempts", totalAttempts).
			Float64("error_rate", errorRate).
			Int("allowed", allowed).
			Float64("threshold", u.errorRateThreshold).
			Float64("step_percent", u.stepPercent).
			Msgf("auto-tune: reducing rate limit for upstream %s due to errors", u.upstreamId)
	} else if errors == 0 && (peakUtilization > 0.95 || rateLimitedRate > u.errorRateThreshold) {
		newDirection = directionIncrease
		newLimit = u.applyAdaptiveStep(oldLimit, newDirection)

		log.Info().
			Int("old_limit", oldLimit).
			Int("new_limit", newLimit).
			Float64("error_rate", errorRate).
			Int("peak_requests", peakRequests).
			Float64("peak_utilization", peakUtilization).
			Float64("rate_limited_rate", rateLimitedRate).
			Int("total_attempts", totalAttempts).
			Int("allowed", allowed).
			Float64("step_percent", u.stepPercent).
			Msgf("auto-tune: increasing rate limit for upstream %s", u.upstreamId)
	} else {
		log.Debug().
			Int("old_limit", oldLimit).
			Int("new_limit", newLimit).
			Int("peak_requests", peakRequests).
			Float64("peak_utilization", peakUtilization).
			Float64("rate_limited_rate", rateLimitedRate).
			Int("total_attempts", totalAttempts).
			Int("allowed", allowed).
			Float64("error_rate", errorRate).
			Float64("step_percent", u.stepPercent).
			Msgf("auto-tune: no change in rate limit for upstream %s", u.upstreamId)
	}

	if newLimit < 1 {
		newLimit = 1
	}

	if newLimit != oldLimit {
		TunedRateLimitMetric.WithLabelValues(u.upstreamId, u.rateLimitPeriod.String()).Set(float64(newLimit))
		u.ratelimit.Store(int32(newLimit))
		u.bucket.Store(ratelimit.NewBucketWithQuantum(u.rateLimitPeriod, int64(newLimit), int64(newLimit)))
	}
}

func (u *UpstreamAutoTune) IncErrors() {
	u.accumErrors.Add(1)
}

func (u *UpstreamAutoTune) applyAdaptiveStep(currentLimit int, newDirection direction) int {
	lastDir := u.lastDirection
	currentStepPercent := u.stepPercent

	var newStepPercent float64

	if newDirection == lastDir && lastDir != directionStable {
		newStepPercent = math.Min(currentStepPercent*2.0, 50.0)
	} else if newDirection != lastDir && lastDir != directionStable {
		newStepPercent = math.Max(currentStepPercent/2.0, 2.5)
	} else {
		newStepPercent = 10.0
	}

	u.stepPercent = newStepPercent
	u.lastDirection = newDirection

	var factor float64
	if newDirection == directionDecrease {
		factor = 1.0 - newStepPercent/100.0
	} else {
		factor = 1.0 + newStepPercent/100.0
	}

	return int(math.Ceil(float64(currentLimit) * factor))
}
