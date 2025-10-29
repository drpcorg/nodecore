package ratelimiter

import (
	"github.com/juju/ratelimit"
)

type RateLimitMemoryEngine struct {
	limiters map[string]*ratelimit.Bucket
}

func NewRateLimitMemoryEngine() *RateLimitMemoryEngine {
	return &RateLimitMemoryEngine{
		limiters: make(map[string]*ratelimit.Bucket),
	}
}

func (e *RateLimitMemoryEngine) Execute(cmd []RateLimitCommand) (bool, error) {
	result := true
	for _, cmd := range cmd {
		limiter, ok := e.limiters[cmd.Name]
		if !ok {
			switch tp := cmd.Type.(type) {
			case *FixedRateLimiterType:
				limiter = ratelimit.NewBucketWithQuantum(tp.period, int64(tp.requests), int64(tp.requests))

			}
		}

		result = result && limiter.TakeAvailable(1) > 0
		e.limiters[cmd.Name] = limiter
	}
	return result, nil
}
