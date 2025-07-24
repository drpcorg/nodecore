package resilience

import "github.com/failsafe-go/failsafe-go/common"

func failureResult[R any](err error) *common.PolicyResult[R] {
	return &common.PolicyResult[R]{
		Error: err,
		Done:  true,
	}
}

type number interface {
	~int | ~int64 | ~uint | ~uint64
}

func randomDelayInRange[T number](delayMin T, delayMax T, random float64) T {
	min64 := float64(delayMin)
	max64 := float64(delayMax)
	return T(random*(max64-min64) + min64)
}

func randomDelay[T number](delay T, jitter T, random float64) T {
	randomAddend := (1 - random*2) * float64(jitter)
	return delay + T(randomAddend)
}

func randomDelayFactor[T number](delay T, jitterFactor float32, random float32) T {
	randomFactor := 1 + (1-random*2)*jitterFactor
	return T(float32(delay) * randomFactor)
}
