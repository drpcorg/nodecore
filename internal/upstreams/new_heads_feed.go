package upstreams

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/prometheus/client_golang/prometheus"
)

// newHeadsFeedWindow is how many recent chain head heights a late subscription head can still fill.
const newHeadsFeedWindow = 128

var newHeadsLateMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "new_heads",
		Name:      "announced_late_total",
		Help:      "Heads announced to newHeads subscribers after the chain head took the block from a source without a subscription payload",
	},
	[]string{"chain"},
)

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
	prometheus.MustRegister(newHeadsLateMetric, newHeadsSkippedMetric)
}

// newHeadsFeed picks the heads announced to newHeads subscribers: one subscription payload
// (RawData) per chain head height, in order. When the fork choice takes a height from a head
// without a payload (polled, manual), the height is announced once a subscription head with
// the same block arrives. Not safe for concurrent use: driven by the supervisor event loop.
type newHeadsFeed struct {
	chain string
	// chainHeads holds recent chain head hashes by height; an empty hash marks a height
	// the chain head jumped over.
	chainHeads map[uint64]blockchain.HashId
	announced  map[uint64]blockchain.HashId
	head       protocol.Block
	top        uint64 // last announced height
}

func newNewHeadsFeed(chain string) *newHeadsFeed {
	return &newHeadsFeed{
		chain:      chain,
		chainHeads: make(map[uint64]blockchain.HashId),
		announced:  make(map[uint64]blockchain.HashId),
	}
}

// onHead returns the head to announce after an upstream head event, if any.
func (f *newHeadsFeed) onHead(event *protocol.HeadUpstreamEvent, chainHeadChanged bool, chainHead protocol.Block) (protocol.Block, bool) {
	if chainHeadChanged && !chainHead.IsEmptyByHeight() {
		f.trackChainHead(chainHead)
		if len(chainHead.RawData) > 0 {
			return f.announce(chainHead)
		}
	}

	block := event.Head
	if event.Status != protocol.Available || len(block.RawData) == 0 || block.Height <= f.top {
		return protocol.Block{}, false
	}
	hash, ok := f.chainHeads[block.Height]
	if !ok || (len(hash) > 0 && !hash.Equals(block.Hash)) {
		return protocol.Block{}, false
	}
	announced, ok := f.announce(block)
	if ok {
		newHeadsLateMetric.WithLabelValues(f.chain).Inc()
	}
	return announced, ok
}

func (f *newHeadsFeed) trackChainHead(head protocol.Block) {
	if head.Height < f.head.Height || (head.Height == f.head.Height && !head.Hash.Equals(f.head.Hash)) {
		// the chain moved down or switched blocks: heights above the new head are no longer on it
		for height := range f.chainHeads {
			if height > head.Height {
				delete(f.chainHeads, height)
			}
		}
		for height := range f.announced {
			if height > head.Height {
				delete(f.announced, height)
			}
		}
		if hash, ok := f.announced[head.Height]; ok && !hash.Equals(head.Hash) {
			delete(f.announced, head.Height)
		}
		if _, ok := f.announced[head.Height]; ok {
			f.top = head.Height
		} else if head.Height > 0 {
			f.top = min(f.top, head.Height-1)
		}
	} else if f.head.Height > 0 && head.Height > f.head.Height+1 {
		from := max(f.head.Height+1, head.Height+1-min(head.Height, newHeadsFeedWindow))
		for height := from; height < head.Height; height++ {
			f.chainHeads[height] = nil
		}
	}
	f.chainHeads[head.Height] = head.Hash
	f.head = head

	for height := range f.chainHeads {
		if height+newHeadsFeedWindow <= head.Height {
			delete(f.chainHeads, height)
		}
	}
	for height := range f.announced {
		if height+newHeadsFeedWindow <= head.Height {
			delete(f.announced, height)
		}
	}
}

func (f *newHeadsFeed) announce(block protocol.Block) (protocol.Block, bool) {
	if hash, ok := f.announced[block.Height]; ok && hash.Equals(block.Hash) {
		return protocol.Block{}, false
	}
	if f.top > 0 && block.Height > f.top+1 {
		newHeadsSkippedMetric.WithLabelValues(f.chain).Add(float64(block.Height - f.top - 1))
	}
	f.announced[block.Height] = block.Hash
	f.top = block.Height
	return block, true
}
