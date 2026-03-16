package lower_bounds

import (
	"math"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/sajari/regression"
)

const maxBounds = 3

type zero = protocol.LowerBoundData

type LowerBounds struct {
	averageSpeed float64

	lowerBounds *utils.CMap[protocol.LowerBoundType, *LowerBoundCoeffs]
}

func NewLowerBounds(averageSpeed float64) *LowerBounds {
	return &LowerBounds{
		averageSpeed: averageSpeed,
		lowerBounds:  utils.NewCMap[protocol.LowerBoundType, *LowerBoundCoeffs](),
	}
}

func (lb *LowerBounds) UpdateBound(newBound protocol.LowerBoundData) {
	coeffs, ok := lb.lowerBounds.Load(newBound.Type)
	if !ok {
		lb.initBound(newBound)
		return
	}

	lastBound := coeffs.getLastBound()
	if newBound.Timestamp == lastBound.Timestamp {
		return
	}

	switch {
	case newBound.Bound == 1:
		lb.resetBound(coeffs, newBound, 0.0, 1.0)

	case newBound.Bound < lastBound.Bound || newBound.Bound-lastBound.Bound >= 100000:
		lb.resetBound(coeffs, newBound, lb.averageSpeed, lb.calculateB(newBound))

	default:
		lb.appendBound(coeffs, newBound)
		if coeffs.boundsSize() < maxBounds {
			coeffs.updateCoeffs(lb.averageSpeed, lb.calculateB(newBound))
		} else {
			coeffs.train(lb.averageSpeed)
		}
	}
}

func (lb *LowerBounds) initBound(newBound protocol.LowerBoundData) {
	coeffs := NewLowerBoundCoeffs()
	coeffs.addBound(newBound)

	if newBound.Bound == 1 {
		coeffs.updateCoeffs(0.0, 1.0)
	} else {
		coeffs.updateCoeffs(lb.averageSpeed, lb.calculateB(newBound))
	}

	lb.lowerBounds.Store(newBound.Type, coeffs)
}

func (lb *LowerBounds) resetBound(
	coeffs *LowerBoundCoeffs,
	newBound protocol.LowerBoundData,
	a float64,
	b float64,
) {
	coeffs.updateCoeffs(a, b)
	coeffs.clearBounds()
	coeffs.addBound(newBound)
}

func (lb *LowerBounds) appendBound(coeffs *LowerBoundCoeffs, newBound protocol.LowerBoundData) {
	if coeffs.boundsSize() == maxBounds {
		coeffs.removeFirst()
	}
	coeffs.addBound(newBound)
}

func (lb *LowerBounds) PredictNextBound(boundType protocol.LowerBoundType, timeOffsetSeconds int64) int64 {
	coeffs, ok := lb.lowerBounds.Load(boundType)
	if !ok {
		return 0
	}

	xTime := time.Now().Unix() + timeOffsetSeconds
	k, b := coeffs.getCoeffs()

	return int64(math.Round(k*float64(xTime) + b))
}

func (lb *LowerBounds) PredictNextBoundAtSpecificTime(boundType protocol.LowerBoundType, timestamp int64) int64 {
	coeffs, ok := lb.lowerBounds.Load(boundType)
	if !ok {
		return 0
	}

	k, b := coeffs.getCoeffs()
	return int64(math.Round(k*float64(timestamp) + b))
}

func (lb *LowerBounds) GetLastBound(boundType protocol.LowerBoundType) (protocol.LowerBoundData, bool) {
	coeffs, ok := lb.lowerBounds.Load(boundType)
	if !ok {
		return zero{}, false
	}

	return coeffs.getLastBound(), true
}

func (lb *LowerBounds) GetAllBounds(boundType protocol.LowerBoundType) []protocol.LowerBoundData {
	coeffs, ok := lb.lowerBounds.Load(boundType)
	if !ok {
		return nil
	}

	return coeffs.getAllBounds()
}

func (lb *LowerBounds) calculateB(bound protocol.LowerBoundData) float64 {
	return float64(bound.Bound) - (lb.averageSpeed * float64(bound.Timestamp))
}

// to predict the next lower bound we use linear regression, y = kx + b,
// where x - current time, y - the predicted bound.
type LowerBoundCoeffs struct {
	mu sync.RWMutex

	lowerBounds []protocol.LowerBoundData
	k           float64
	b           float64
}

func NewLowerBoundCoeffs() *LowerBoundCoeffs {
	return &LowerBoundCoeffs{
		lowerBounds: make([]protocol.LowerBoundData, 0, maxBounds),
	}
}

func (c *LowerBoundCoeffs) addBound(bound protocol.LowerBoundData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lowerBounds = append(c.lowerBounds, bound)
}

func (c *LowerBoundCoeffs) updateCoeffs(newK, newB float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.k = newK
	c.b = newB
}

func (c *LowerBoundCoeffs) getCoeffs() (float64, float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.k, c.b
}

func (c *LowerBoundCoeffs) clearBounds() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lowerBounds = c.lowerBounds[:0]
}

func (c *LowerBoundCoeffs) removeFirst() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.lowerBounds) == 0 {
		return
	}

	c.lowerBounds = c.lowerBounds[1:]
}

func (c *LowerBoundCoeffs) boundsSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.lowerBounds)
}

func (c *LowerBoundCoeffs) getLastBound() protocol.LowerBoundData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.lowerBounds[len(c.lowerBounds)-1]
}

func (c *LowerBoundCoeffs) getAllBounds() []protocol.LowerBoundData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]protocol.LowerBoundData, len(c.lowerBounds))
	copy(res, c.lowerBounds)
	return res
}

func (c *LowerBoundCoeffs) train(averageSpeed float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := new(regression.Regression)
	points := regression.DataPoints{}
	lastLowerBound := c.lowerBounds[len(c.lowerBounds)-1]

	for _, lowerBound := range c.lowerBounds {
		points = append(points, regression.DataPoint(float64(lowerBound.Bound), []float64{float64(lowerBound.Timestamp)}))
	}

	r.Train(points...)
	err := r.Run()
	if err != nil {
		log.Err(err).Msg("Couldn't train the data set")
		c.k = averageSpeed
		c.b = float64(lastLowerBound.Bound) - averageSpeed*float64(lastLowerBound.Timestamp)
	} else {
		coeffs := r.GetCoeffs()
		// we want our line to go through the last point in terms of the function k(y-y(t))+b
		c.k = coeffs[1]
		c.b = float64(lastLowerBound.Bound) - coeffs[1]*float64(lastLowerBound.Timestamp)
	}
}
