package upstreams

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/prometheus/client_golang/prometheus"
)

// newHeadsLagWindow drops ws heads this many blocks behind the chain head.
const newHeadsLagWindow = 128

var newHeadsSkippedMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "new_heads",
		Name:      "skipped_total",
		Help:      "Heights the newHeads feed jumped over without announcing",
	},
	[]string{"chain"},
)

func init() {
	prometheus.MustRegister(newHeadsSkippedMetric)
}

// newHeadsFeed picks the heads announced to newHeads subscribers: every ws head (RawData) above
// the last announced height, whether or not it changed the chain head - a polled upstream often
// reaches a height first. Driven by the supervisor event loop.
type newHeadsFeed struct {
	chain string
	head  uint64 // chain head height
	last  uint64 // last announced height
}

func (f *newHeadsFeed) onHead(event *protocol.HeadUpstreamEvent, chainHeadChanged bool, chainHead protocol.Block) (protocol.Block, bool) {
	if chainHeadChanged {
		// an upstream reported a lower head (reorg): that height can be announced again;
		// a head lowered by an upstream leaving is not a new block
		if event.Status == protocol.Available && chainHead.Height > 0 && chainHead.Height < f.head {
			f.last = min(f.last, chainHead.Height-1)
		}
		f.head = chainHead.Height
	}
	block := event.Head
	if event.Status != protocol.Available || len(block.RawData) == 0 ||
		block.Height <= f.last || block.Height+newHeadsLagWindow <= chainHead.Height {
		return protocol.Block{}, false
	}
	if f.last > 0 && block.Height > f.last+1 {
		newHeadsSkippedMetric.WithLabelValues(f.chain).Add(float64(block.Height - f.last - 1))
	}
	f.last = block.Height
	return block, true
}
