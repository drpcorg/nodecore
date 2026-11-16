package evm_bounds

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// NewEvmBlockLowerBoundDetector detects the earliest available block. On an upstream that
// answers pruned blocks with bare headers it detects the start of the contiguous range of
// blocks, ending at the head, that still have their bodies. The logs bound comes from the
// receipts detector: pruned nodes keep headers from genesis but not receipts.
func NewEvmBlockLowerBoundDetector(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(
		upstreamId,
		chain,
		internalTimeout,
		connector,
		protocol.BlockBound,
		[]protocol.LowerBoundType{protocol.BlockBound},
		0,
	)
}

func (e *EvmLowerBoundDetector) hasBlock(ctx context.Context, height int64) (bool, error) {
	raw, available, err := e.call(ctx, "eth_getBlockByNumber", []any{evmBlockTag(height), false})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

// bareHeadersVerdict says whether the upstream is proven to answer a block whose body it
// pruned with the bare header instead of null (reth with pruned bodies), and by which proof.
type bareHeadersVerdict int32

const (
	bareHeadersUnproven bareHeadersVerdict = iota
	// bareHeadersByGoldBlock is the verdict of the gold block that came back as a bare header
	bareHeadersByGoldBlock
	// bareHeadersNearPlainBound is the verdict of a bare header near the plain bound
	bareHeadersNearPlainBound
)

// goldBlockAnswer is what the gold block tells about the upstream in one cycle.
type goldBlockAnswer int

const (
	// goldBlockUnknown is a chain without a gold transaction, null, a no-data error, a failed
	// request and a block that is neither of the two below
	goldBlockUnknown goldBlockAnswer = iota
	goldBlockBare
	goldBlockWithBody
)

// detectBlockBound picks the search for the block bound. An upstream that answers pruned
// blocks with bare headers gets the body search: the plain one takes its headers for blocks.
// The verdict stays for the cycles that prove nothing, as a proven upstream must not fall back
// to the plain search, while the body search stays right for one that turns to answering null.
//
// The gold block is the first proof. Its bare header proves the upstream, and only that block
// with its transactions clears this verdict. Those transactions also settle the cycle: the
// upstream is seen to return an old block whole, so the plain search result stands and the
// blocks near its bound, which may only look bare, are not asked.
//
// When the gold block tells nothing, a bare header near the plain bound is the second proof,
// see proveNearPlainBound.
func (e *EvmLowerBoundDetector) detectBlockBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	verdict := bareHeadersVerdict(e.bareHeaders.Load())
	switch e.askGoldBlock(ctx) {
	case goldBlockBare:
		verdict = bareHeadersByGoldBlock
		e.setBareHeadersVerdict(verdict)
	case goldBlockWithBody:
		e.setBareHeadersVerdict(bareHeadersUnproven)
		return e.detectPlainBound(ctx)
	}

	switch verdict {
	case bareHeadersByGoldBlock:
		search, err := e.searchBodies(ctx)
		return search.bound, err
	case bareHeadersNearPlainBound:
		return e.recheckNearPlainBound(ctx)
	default:
		plainBound, err := e.detectPlainBound(ctx)
		if err != nil {
			return nil, err
		}
		return e.proveNearPlainBound(ctx, plainBound)
	}
}

// proveNearPlainBound is the second proof: a bare header near the plain bound that
// confirmBareHeaders holds up moves the upstream to the body search. A bound whose blocks
// proved nothing is not asked again until it moves or bareHeaderRecheckInterval passes, as a
// node gets replaced behind its URL. Blocks that could not be read settle nothing: the plain
// bound stands for the cycle and is asked again in the next.
func (e *EvmLowerBoundDetector) proveNearPlainBound(ctx context.Context, plainBound []protocol.LowerBoundData) ([]protocol.LowerBoundData, error) {
	bound := plainBound[0].Bound
	if e.checkedRecently(bound) {
		return plainBound, nil
	}
	bareHeaderAt, decided := e.bareHeaderNearPlainBound(ctx, bound)
	if bareHeaderAt == 0 {
		if decided {
			e.rememberChecked(bound)
		}
		return plainBound, nil
	}
	bodyBound, confirmed, err := e.confirmBareHeaders(ctx, bareHeaderAt)
	switch {
	case err != nil:
		log.Debug().Err(err).Msgf("couldn't check the bare header at block %d of upstream '%s'", bareHeaderAt, e.UpstreamId)
		return plainBound, nil
	case !confirmed:
		e.rememberChecked(bound)
		return plainBound, nil
	}
	e.bareHeaderAt.Store(bareHeaderAt)
	e.setBareHeadersVerdict(bareHeadersNearPlainBound)
	return bodyBound, nil
}

// recheckNearPlainBound is a cycle of an upstream proven by proveNearPlainBound: the body
// search answers while confirmBareHeaders still holds the proof up, and the plain search once
// it does not. Blocks that could not be read fail the cycle and leave the verdict as it is.
func (e *EvmLowerBoundDetector) recheckNearPlainBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	bodyBound, confirmed, err := e.confirmBareHeaders(ctx, e.bareHeaderAt.Load())
	if err != nil || confirmed {
		return bodyBound, err
	}
	e.setBareHeadersVerdict(bareHeadersUnproven)
	return e.detectPlainBound(ctx)
}

// confirmBareHeaders tells a node that pruned bodies from an upstream whose blocks only look
// bare, as on a chain whose transactionsRoot covers more than the listed transactions. The
// node has bodies under the head, which the body search meets, and none from the bare header
// up to the bound of that search: hasBodyBetween asks blocks spread over that stretch. It
// fails when blocks could not be read, as such a cycle settles nothing.
func (e *EvmLowerBoundDetector) confirmBareHeaders(ctx context.Context, bareHeaderAt int64) ([]protocol.LowerBoundData, bool, error) {
	search, err := e.searchBodies(ctx)
	switch {
	case err != nil:
		return nil, false, err
	case !search.bodyFound && search.probeFailed:
		return nil, false, fmt.Errorf("upstream '%s' failed block requests of the body search", e.UpstreamId)
	case !search.bodyFound:
		return nil, false, nil
	}
	bodyBelow, err := e.hasBodyBetween(ctx, bareHeaderAt, search.bound[0].Bound)
	if err != nil || bodyBelow {
		return nil, false, err
	}
	return search.bound, true, nil
}

// hasBodyBetween asks evmBareHeaderSegmentSamples blocks spread between from and to, both
// excluded, for one with transactions. Each is shifted one block further than the one before:
// evenly spaced, they could all fall on bare-looking blocks that come at a regular step.
func (e *EvmLowerBoundDetector) hasBodyBetween(ctx context.Context, from, to int64) (bool, error) {
	step := (to - from) / (evmBareHeaderSegmentSamples + 1)
	asked := from
	for sample := int64(1); sample <= evmBareHeaderSegmentSamples; sample++ {
		height := from + step*sample + sample - 1
		if height <= asked || height >= to {
			continue
		}
		asked = height
		block, available, err := e.blockAt(ctx, height)
		if err != nil {
			return false, err
		}
		if available && len(block.Transactions) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// checkedRecently reports a plain bound whose blocks proved nothing less than
// bareHeaderRecheckInterval ago.
func (e *EvmLowerBoundDetector) checkedRecently(bound int64) bool {
	checkedAt := time.Unix(0, e.bareHeaderCheckedAt.Load())
	return bound == e.bareHeaderCheckedBound.Load() && time.Since(checkedAt) < e.bareHeaderRecheckInterval
}

func (e *EvmLowerBoundDetector) rememberChecked(bound int64) {
	e.bareHeaderCheckedBound.Store(bound)
	e.bareHeaderCheckedAt.Store(time.Now().UnixNano())
}

// SetBareHeaderRecheckInterval overrides how long a plain bound whose blocks proved nothing
// is left alone. Production relies on the default; tests shrink it.
func (e *EvmLowerBoundDetector) SetBareHeaderRecheckInterval(interval time.Duration) {
	e.bareHeaderRecheckInterval = interval
}

// setBareHeadersVerdict stores the verdict and logs it when it changes.
func (e *EvmLowerBoundDetector) setBareHeadersVerdict(verdict bareHeadersVerdict) {
	if bareHeadersVerdict(e.bareHeaders.Swap(int32(verdict))) == verdict {
		return
	}
	switch verdict {
	case bareHeadersByGoldBlock:
		log.Info().Msgf("upstream '%s' answers pruned blocks with bare headers, as its gold block shows: the block lower bound is the first block with a body", e.UpstreamId)
	case bareHeadersNearPlainBound:
		log.Info().Msgf("upstream '%s' answers pruned blocks with bare headers, as a block near its plain lower bound shows: the block lower bound is the first block with a body", e.UpstreamId)
	default:
		log.Info().Msgf("upstream '%s' is no longer taken to answer pruned blocks with bare headers: the block lower bound comes from the plain search", e.UpstreamId)
	}
}

func (e *EvmLowerBoundDetector) detectPlainBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return e.LowerBoundSearchCalculator.DetectLowerBound(ctx, e.fetchLatestHeight, e.probe)
}

// bodySearch is the outcome of one body search.
type bodySearch struct {
	bound []protocol.LowerBoundData
	// bodyFound reports a probe that met a block with its transactions
	bodyFound bool
	// probeFailed reports a probe request that failed: after its retries the search takes
	// such a block for one without a body
	probeFailed bool
}

func (e *EvmLowerBoundDetector) searchBodies(ctx context.Context) (bodySearch, error) {
	search := bodySearch{}
	probe := func(ctx context.Context, height int64) (lower_bounds.ContiguousProbeResult, error) {
		result, err := e.probeBlockBody(ctx, height)
		search.bodyFound = search.bodyFound || result == lower_bounds.ProbeHit
		search.probeFailed = search.probeFailed || err != nil
		return result, err
	}
	bound, err := e.DetectContiguousLowerBoundWithOffset(ctx, e.fetchLatestHeight, probe, evmLowerBoundMaxOffset)
	search.bound = bound
	return search, err
}

// askGoldBlock reads the block of the chain's gold transaction. It is known to hold one, so
// a bare header there has lost its body.
func (e *EvmLowerBoundDetector) askGoldBlock(ctx context.Context) goldBlockAnswer {
	height, ok := e.goldTxBlock()
	if !ok {
		return goldBlockUnknown
	}
	block, available, err := e.fetchGoldBlock(ctx, height)
	switch {
	case err != nil || !available:
		return goldBlockUnknown
	case block.isBareHeader():
		return goldBlockBare
	case len(block.Transactions) > 0:
		return goldBlockWithBody
	default:
		return goldBlockUnknown
	}
}

// bareHeaderNearPlainBound asks the blocks from the plain bound up, evmBareHeaderProofWindow
// past it at most, for a bare header and returns its height, 0 when there is none. A node
// restored from a snapshot answers null below the snapshot, the gold block included, and bare
// headers from it on, so the plain search stops at the first of them. A block with
// transactions ends the search: bodies start where the plain search says. A bare header
// counts only with gas used: neither the root nor the gas alone tells a pruned block from an
// empty one on every chain, see TestEvmBlockLowerBoundDetectorTakesNoEmptyBlockForBareHeader.
//
// decided is false when the blocks told nothing and a request failed or the context ended:
// the same bound is worth asking again. Null is an answer and leaves the blocks decided.
func (e *EvmLowerBoundDetector) bareHeaderNearPlainBound(ctx context.Context, bound int64) (bareHeaderAt int64, decided bool) {
	decided = true
	for height := bound; height <= bound+evmBareHeaderProofWindow; height++ {
		if ctx.Err() != nil {
			return 0, false
		}
		block, available, err := e.blockAt(ctx, height)
		switch {
		case err != nil:
			decided = false
		case !available:
			// null tells nothing
		case len(block.Transactions) > 0:
			return 0, true
		case block.isBareHeader() && block.hasGasUsed():
			return height, true
		}
	}
	return 0, decided
}

// fetchGoldBlock fetches the gold block, repeating a failed request: evmGoldBlockAttempts
// attempts in all, goldBlockRetryDelay apart.
func (e *EvmLowerBoundDetector) fetchGoldBlock(ctx context.Context, height int64) (evmBlockEnvelope, bool, error) {
	for attempt := 1; ; attempt++ {
		block, available, err := e.blockAt(ctx, height)
		if err == nil || attempt >= evmGoldBlockAttempts {
			return block, available, err
		}
		select {
		case <-ctx.Done():
			return evmBlockEnvelope{}, false, ctx.Err()
		case <-time.After(e.goldBlockRetryDelay):
		}
	}
}

// SetGoldBlockRetryDelay overrides the pause between attempts to fetch the gold block.
// Production relies on the default; tests shrink it.
func (e *EvmLowerBoundDetector) SetGoldBlockRetryDelay(delay time.Duration) {
	e.goldBlockRetryDelay = delay
}

// probeBlockBody is the probe of the body search on an upstream that serves bare headers: a
// block with transactions is a hit and a bare header is a miss. A block that really has no
// transactions is ProbeEmpty: it looks the same with and without its body.
func (e *EvmLowerBoundDetector) probeBlockBody(ctx context.Context, height int64) (lower_bounds.ContiguousProbeResult, error) {
	block, available, err := e.blockAt(ctx, height)
	if err != nil || !available {
		return lower_bounds.ProbeMiss, err
	}
	switch {
	case len(block.Transactions) > 0:
		return lower_bounds.ProbeHit, nil
	case block.isBareHeader():
		return lower_bounds.ProbeMiss, nil
	default:
		return lower_bounds.ProbeEmpty, nil
	}
}
