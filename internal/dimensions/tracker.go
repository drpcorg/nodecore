package dimensions

import (
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"sync/atomic"
)

type DimensionTracker struct {
	upstreamDimensionsMap *utils.CMap[upstreamDimensionKey, UpstreamDimensions]
	chainDimensionsMap    *utils.CMap[chainDimensionKey, ChainDimensions]
}

func NewDimensionTracker() *DimensionTracker {
	return &DimensionTracker{
		upstreamDimensionsMap: utils.NewCMap[upstreamDimensionKey, UpstreamDimensions](),
		chainDimensionsMap:    utils.NewCMap[chainDimensionKey, ChainDimensions](),
	}
}

func (d *DimensionTracker) GetAllDimensions(chain chains.Chain, upstreamId, method string) *FullDimensions {
	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(newUpstreamDimensionKey(chain, upstreamId, method), NewDimensions())
	chainDims, _ := d.chainDimensionsMap.LoadOrStore(newChainDimensionKey(chain, upstreamId), &ChainDimensions{})
	return &FullDimensions{
		UpstreamDimensions: upstreamDimensions,
		ChainDimensions:    chainDims,
	}
}

func (d *DimensionTracker) GetUpstreamDimensions(chain chains.Chain, upstreamId, method string) *UpstreamDimensions {
	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(newUpstreamDimensionKey(chain, upstreamId, method), NewDimensions())
	return upstreamDimensions
}

func (d *DimensionTracker) GetChainDimensions(chain chains.Chain, upstreamId string) *ChainDimensions {
	chainDimensions, _ := d.chainDimensionsMap.LoadOrStore(newChainDimensionKey(chain, upstreamId), &ChainDimensions{})
	return chainDimensions
}

func (d *DimensionTracker) TrackLags(chain chains.Chain, upstreamId string, headLag, finalizationLag uint64) {
	chainDims, _ := d.chainDimensionsMap.LoadOrStore(newChainDimensionKey(chain, upstreamId), &ChainDimensions{})
	chainDims.headLag.Store(headLag)
	chainDims.finalizationLag.Store(finalizationLag)
}

type FullDimensions struct {
	ChainDimensions    *ChainDimensions
	UpstreamDimensions *UpstreamDimensions
}

type ChainDimensions struct {
	headLag         atomic.Uint64
	finalizationLag atomic.Uint64
}

func (c *ChainDimensions) GetHeadLag() uint64 {
	return c.headLag.Load()
}

func (c *ChainDimensions) GetFinalizationLag() uint64 {
	return c.finalizationLag.Load()
}

type UpstreamDimensions struct {
	quantileTracker   *quantileTracker
	totalRequests     atomic.Uint64
	totalErrors       atomic.Uint64
	successfulRetries atomic.Uint64
}

func (d *UpstreamDimensions) TrackSuccessfulRetries() {
	d.successfulRetries.Add(1)
}

func (d *UpstreamDimensions) TrackRequestDuration(duration float64) {
	d.quantileTracker.add(duration)
}

func (d *UpstreamDimensions) TrackTotalRequests() {
	d.totalRequests.Add(1)
}

func (d *UpstreamDimensions) TrackTotalErrors() {
	d.totalErrors.Add(1)
}

func (d *UpstreamDimensions) GetSuccessfulRetries() uint64 {
	return d.successfulRetries.Load()
}

func (d *UpstreamDimensions) GetTotalRequests() uint64 {
	return d.totalRequests.Load()
}

func (d *UpstreamDimensions) GetTotalErrors() uint64 {
	return d.totalErrors.Load()
}

func (d *UpstreamDimensions) GetErrorRate() float64 {
	totalRequests := d.totalRequests.Load()
	if totalRequests == 0 {
		return 0
	}
	return float64(d.totalErrors.Load()) / float64(totalRequests)
}

func (d *UpstreamDimensions) GetValueAtQuantile(quantile float64) float64 {
	return d.quantileTracker.getValueAtQuantile(quantile).Seconds()
}

func NewDimensions() *UpstreamDimensions {
	return &UpstreamDimensions{
		quantileTracker: newQuantileTracker(),
	}
}

type upstreamDimensionKey struct {
	chain      chains.Chain
	upstreamId string
	method     string
}

func newUpstreamDimensionKey(chain chains.Chain, upstreamId, method string) upstreamDimensionKey {
	return upstreamDimensionKey{
		chain:      chain,
		upstreamId: upstreamId,
		method:     method,
	}
}

type chainDimensionKey struct {
	chain      chains.Chain
	upstreamId string
}

func newChainDimensionKey(chain chains.Chain, upstreamId string) chainDimensionKey {
	return chainDimensionKey{
		chain:      chain,
		upstreamId: upstreamId,
	}
}
