package dimensions

import (
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/rs/zerolog/log"
	"sync"
	"time"
)

type quantileTracker struct {
	sketch *ddsketch.DDSketch
	mu     sync.RWMutex
}

func newQuantileTracker() *quantileTracker {
	sketch, _ := ddsketch.NewDefaultDDSketch(0.01)
	return &quantileTracker{
		sketch: sketch,
	}
}

func (q *quantileTracker) add(value float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.sketch.Add(value); err != nil {
		log.Warn().Err(err).Msgf("couldn't add a value %f to a quantile tracker", value)
	}
}

func (q *quantileTracker) getValueAtQuantile(quantile float64) time.Duration {
	q.mu.RLock()
	defer q.mu.RUnlock()
	value, err := q.sketch.GetValueAtQuantile(quantile)
	if err != nil {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}
