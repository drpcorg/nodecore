package lower_bounds

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const aztecPeriod = 5 * time.Minute

// aztecMaxProbe caps the exponential probe used to find an upper bound when the
// upstream is not an archive node. Aztec mainnet block numbers grow at ~1 block/s,
// so 100M is multiple decades of headroom and serves as a safety stop only.
const aztecMaxProbe int64 = 100_000_000

var errAztecNoBlock = errors.New("aztec upstream has no blocks within probing range")

// AztecLowerBoundDetector binary-searches the lowest L2 block the upstream can
// still serve. node_getBlockHeader(N) returns JSON `null` for blocks the node does
// not have (no error, just null), so the probe treats a `null` (case-insensitive,
// trimmed) result as "no data" and any non-null body as "has data". The header is
// preferred over node_getBlock because we only need a presence signal and the
// binary search performs ~log2(currentHeight) probes per refresh cycle.
//
// Most Aztec full nodes are archive nodes and converge to bound=1 immediately;
// pruning-capable builds will converge to whatever block the prune kept.
type AztecLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewAztecLowerBoundDetector(
	upstreamId string,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AztecLowerBoundDetector {
	return &AztecLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (a *AztecLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	// Fast path: archive nodes have block 1 available.
	if has, err := a.hasBlock(1); err == nil && has {
		return []protocol.LowerBoundData{
			protocol.NewLowerBoundDataNow(1, protocol.StateBound),
		}, nil
	}

	// Find an upper bound by exponential probe.
	var lo int64 = 2
	var hi int64 = 1000
	for {
		has, err := a.hasBlock(hi)
		if err != nil {
			return nil, err
		}
		if has {
			break
		}
		if hi >= aztecMaxProbe {
			return nil, errAztecNoBlock
		}
		lo = hi + 1
		next := hi * 10
		if next > aztecMaxProbe {
			next = aztecMaxProbe
		}
		hi = next
	}

	// Binary search [lo, hi] for the smallest block that has data.
	for lo < hi {
		mid := lo + (hi-lo)/2
		has, err := a.hasBlock(mid)
		if err != nil {
			return nil, err
		}
		if has {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(lo, protocol.StateBound),
	}, nil
}

func (a *AztecLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (a *AztecLowerBoundDetector) Period() time.Duration {
	return aztecPeriod
}

func (a *AztecLowerBoundDetector) hasBlock(number int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getBlockHeader", []interface{}{number}, chains.AZTEC_MAINNET)
	if err != nil {
		return false, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		// RPC errors here usually mean "no block" / "pruned" / temporary upstream
		// issue. Treat as "no data" - the binary search will move past this block.
		// Real connectivity issues are caught by the surrounding retry/health flow.
		return false, nil
	}

	body := strings.TrimSpace(strings.ToLower(string(response.ResponseResult())))
	if body == "" || body == "null" {
		return false, nil
	}
	return true, nil
}

var _ LowerBoundDetector = (*AztecLowerBoundDetector)(nil)
