package dimensions

import (
	"sync/atomic"

	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var DefBuckets = []float64{
	.005,
	.01,
	.025,
	.05,
	.1,
	.25,
	.5,
	1,
	2,
	3,
	4,
	5,
	6,
	7,
	8,
	15,
	25,
	50,
	100,
}

var requestTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "requests_total",
	},
	[]string{"chain", "method", "upstream"},
)

var successfulRetriesMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "successful_retries_total",
	},
	[]string{"chain", "method", "upstream"},
)

var errorTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "errors_total",
	},
	[]string{"chain", "method", "upstream"},
)

var requestDurationMetric = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "request_duration",
		Buckets:   DefBuckets,
	},
	[]string{"chain", "method", "upstream"},
)

var headLagMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "head_lag",
	},
	[]string{"chain", "upstream"},
)

var finalizationLagMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "finalization_lag",
	},
	[]string{"chain", "upstream"},
)

func init() {
	prometheus.MustRegister(
		requestTotalMetric,
		errorTotalMetric,
		requestDurationMetric,
		headLagMetric,
		finalizationLagMetric,
		successfulRetriesMetric,
	)
}

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
	upstreamKey := newUpstreamDimensionKey(chain, upstreamId, method)
	chainKey := newChainDimensionKey(chain, upstreamId)

	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(upstreamKey, newUpstreamDimensions(upstreamKey))
	chainDims, _ := d.chainDimensionsMap.LoadOrStore(chainKey, newChainDimensions(chainKey))
	return &FullDimensions{
		UpstreamDimensions: upstreamDimensions,
		ChainDimensions:    chainDims,
	}
}

func (d *DimensionTracker) GetUpstreamDimensions(chain chains.Chain, upstreamId, method string) *UpstreamDimensions {
	upstreamKey := newUpstreamDimensionKey(chain, upstreamId, method)
	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(upstreamKey, newUpstreamDimensions(upstreamKey))
	return upstreamDimensions
}

func (d *DimensionTracker) GetChainDimensions(chain chains.Chain, upstreamId string) *ChainDimensions {
	chainKey := newChainDimensionKey(chain, upstreamId)

	chainDimensions, _ := d.chainDimensionsMap.LoadOrStore(chainKey, newChainDimensions(chainKey))
	return chainDimensions
}

func (d *DimensionTracker) TrackLags(chain chains.Chain, upstreamId string, headLag, finalizationLag uint64) {
	chainKey := newChainDimensionKey(chain, upstreamId)

	chainDims, _ := d.chainDimensionsMap.LoadOrStore(chainKey, newChainDimensions(chainKey))
	chainDims.headLag.Store(headLag)
	chainDims.finalizationLag.Store(finalizationLag)

	headLagMetric.WithLabelValues(chainKey.chain.String(), chainKey.upstreamId).Set(float64(headLag))
	finalizationLagMetric.WithLabelValues(chainKey.chain.String(), chainKey.upstreamId).Set(float64(finalizationLag))
}

type FullDimensions struct {
	ChainDimensions    *ChainDimensions
	UpstreamDimensions *UpstreamDimensions
}

type ChainDimensions struct {
	headLag         atomic.Uint64
	finalizationLag atomic.Uint64
	key             *utils.Atomic[chainDimensionKey]
}

func newChainDimensions(key chainDimensionKey) *ChainDimensions {
	chainKey := utils.NewAtomic[chainDimensionKey]()
	chainKey.Store(key)
	return &ChainDimensions{
		key: chainKey,
	}
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
	key               *utils.Atomic[upstreamDimensionKey]
}

func (d *UpstreamDimensions) TrackSuccessfulRetries() {
	d.successfulRetries.Add(1)
	key := d.key.Load()
	successfulRetriesMetric.WithLabelValues(key.chain.String(), key.method, key.upstreamId).Inc()
}

func (d *UpstreamDimensions) TrackRequestDuration(duration float64) {
	d.quantileTracker.add(duration)
	key := d.key.Load()
	requestDurationMetric.WithLabelValues(key.chain.String(), key.method, key.upstreamId).Observe(duration)
}

func (d *UpstreamDimensions) TrackTotalRequests() {
	d.totalRequests.Add(1)
	key := d.key.Load()
	requestTotalMetric.WithLabelValues(key.chain.String(), key.method, key.upstreamId).Inc()
}

func (d *UpstreamDimensions) TrackTotalErrors() {
	d.totalErrors.Add(1)
	key := d.key.Load()
	errorTotalMetric.WithLabelValues(key.chain.String(), key.method, key.upstreamId).Inc()
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

func newUpstreamDimensions(key upstreamDimensionKey) *UpstreamDimensions {
	upstreamKey := utils.NewAtomic[upstreamDimensionKey]()
	upstreamKey.Store(key)
	return &UpstreamDimensions{
		quantileTracker: newQuantileTracker(),
		key:             upstreamKey,
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
