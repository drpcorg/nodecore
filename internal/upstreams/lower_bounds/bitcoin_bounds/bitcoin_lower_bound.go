package bitcoin_bounds

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// The prune point moves slowly (bitcoind trims in large chunks), so a long
// period is enough.
const bitcoinPeriod = 15 * time.Minute

var errBitcoinNoPruneHeight = errors.New("bitcoin node is pruned but reported no pruneheight")

var bitcoinEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.BlockBound,
	protocol.TxBound,
}

type BitcoinLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewBitcoinLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *BitcoinLowerBoundDetector {
	return &BitcoinLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (b *BitcoinLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	info, err := b.fetchBlockchainInfo(ctx)
	if err != nil {
		return b.fallback(fmt.Errorf("cannot fetch blockchain info: %w", err)), nil
	}

	bound := int64(1)
	if info.Pruned {
		if info.PruneHeight <= 0 {
			return b.fallback(errBitcoinNoPruneHeight), nil
		}
		bound = info.PruneHeight
	}
	b.lastBound.Store(bound)

	return bitcoinBounds(bound), nil
}

func (b *BitcoinLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, bitcoinEmittedBoundTypes...), protocol.UnknownBound)
}

func (b *BitcoinLowerBoundDetector) Period() time.Duration {
	return bitcoinPeriod
}

// fallback decides what to publish when the calculation cannot complete.
// If a previous tick produced a bound, re-emit it so the router keeps using
// the last known good value. Otherwise emit UnknownBound=0 so consumers get
// an explicit "we don't know" signal instead of silence.
func (b *BitcoinLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := b.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"bitcoin upstream '%s' lower-bound calculation failed; retaining cached bound=%d",
			b.upstreamId, cached,
		)
		return bitcoinBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"bitcoin upstream '%s' lower-bound calculation failed and no cache available; emitting UnknownBound",
		b.upstreamId,
	)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

func bitcoinBounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(bitcoinEmittedBoundTypes))
	for _, bt := range bitcoinEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
}

func (b *BitcoinLowerBoundDetector) fetchBlockchainInfo(ctx context.Context) (*bitcoinBlockchainInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, b.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getblockchaininfo", []any{}, b.chain)
	if err != nil {
		return nil, err
	}

	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, fmt.Errorf("bitcoin upstream '%s' getblockchaininfo returned an empty body", b.upstreamId)
	}
	var info bitcoinBlockchainInfo
	if err := sonic.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("bitcoin upstream '%s' getblockchaininfo unparseable: %w", b.upstreamId, err)
	}
	return &info, nil
}

type bitcoinBlockchainInfo struct {
	Pruned      bool  `json:"pruned"`
	PruneHeight int64 `json:"pruneheight"`
}

var _ lower_bounds.LowerBoundDetector = (*BitcoinLowerBoundDetector)(nil)
