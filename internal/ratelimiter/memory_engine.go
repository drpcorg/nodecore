package ratelimiter

import (
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/juju/ratelimit"
)

type RateLimitMemoryEngine struct {
	limiters utils.CMap[string, ratelimit.Bucket]
}

func NewRateLimitMemoryEngine() *RateLimitMemoryEngine {
	return &RateLimitMemoryEngine{
		limiters: utils.CMap[string, ratelimit.Bucket]{},
	}
}

func (e *RateLimitMemoryEngine) Execute(cmd []RateLimitCommand) (bool, error) {
	result := true
	for _, cmd := range cmd {
		limiter, ok := e.limiters.Load(cmd.Name)

		if !ok {
			var newLimiter *ratelimit.Bucket
			switch tp := cmd.Type.(type) {
			case *FixedRateLimiterType:
				newLimiter = ratelimit.NewBucketWithQuantum(tp.period, int64(tp.requests), int64(tp.requests))
			}

			limiter, _ = e.limiters.LoadOrStore(cmd.Name, newLimiter)
		}

		result = result && limiter.TakeAvailable(1) > 0
	}
	return result, nil
}
