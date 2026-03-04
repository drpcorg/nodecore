package dimensions

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var DefBuckets = []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 25, 50}

var requestTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "requests_total",
		Help:      "The total number of RPC requests sent to an upstream",
	},
	[]string{"chain", "method", "upstream"},
)

var successfulRetriesMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "successful_retries_total",
		Help:      "The total number of RPC requests that succeeded after being retried",
	},
	[]string{"chain", "method", "upstream"},
)

var errorTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "errors_total",
		Help:      "The total number of RPC request errors returned by an upstream",
	},
	[]string{"chain", "method", "upstream"},
)

var requestDurationMetric = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "request_duration",
		Buckets:   DefBuckets,
		Help:      "The duration of RPC requests to upstreams",
	},
	[]string{"chain", "method", "upstream"},
)

var headLagMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "head_lag",
		Help:      "The block lag of an upstream compared to the current head",
	},
	[]string{"chain", "upstream"},
)

var finalizationLagMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "finalization_lag",
		Help:      "The block lag of an upstream compared to the current finalization",
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

type DimensionTracker interface {
	GetAllDimensions(chain chains.Chain, upstreamId, method string) *FullDimensions
	GetUpstreamDimensions(chain chains.Chain, upstreamId, method string) *UpstreamDimensions
	GetChainDimensions(chain chains.Chain, upstreamId string) *ChainDimensions
}

type BaseDimensionTracker struct {
	upstreamDimensionsMap *utils.CMap[upstreamDimensionKey, *UpstreamDimensions]
	chainDimensionsMap    *utils.CMap[chainDimensionKey, *ChainDimensions]
}

func NewBaseDimensionTracker() DimensionTracker {
	return &BaseDimensionTracker{
		upstreamDimensionsMap: utils.NewCMap[upstreamDimensionKey, *UpstreamDimensions](),
		chainDimensionsMap:    utils.NewCMap[chainDimensionKey, *ChainDimensions](),
	}
}

func (d *BaseDimensionTracker) GetAllDimensions(chain chains.Chain, upstreamId, method string) *FullDimensions {
	upstreamKey := newUpstreamDimensionKey(chain, upstreamId, method)
	chainKey := newChainDimensionKey(chain, upstreamId)

	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(upstreamKey, newUpstreamDimensions(upstreamKey))
	chainDims, _ := d.chainDimensionsMap.LoadOrStore(chainKey, newChainDimensions(chainKey))
	return &FullDimensions{
		UpstreamDimensions: upstreamDimensions,
		ChainDimensions:    chainDims,
	}
}

func (d *BaseDimensionTracker) GetUpstreamDimensions(chain chains.Chain, upstreamId, method string) *UpstreamDimensions {
	upstreamKey := newUpstreamDimensionKey(chain, upstreamId, method)
	upstreamDimensions, _ := d.upstreamDimensionsMap.LoadOrStore(upstreamKey, newUpstreamDimensions(upstreamKey))
	return upstreamDimensions
}

func (d *BaseDimensionTracker) GetChainDimensions(chain chains.Chain, upstreamId string) *ChainDimensions {
	chainKey := newChainDimensionKey(chain, upstreamId)

	chainDimensions, _ := d.chainDimensionsMap.LoadOrStore(chainKey, newChainDimensions(chainKey))
	return chainDimensions
}

var _ DimensionTracker = (*BaseDimensionTracker)(nil)
