package evm_methods

import (
	"context"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// probedMethods are the methods whose module prefix does not settle the question: a node
// can report the containing module as present and still not implement them, so each is
// confirmed with a direct call instead of by module attribution.
//
// Everything here MUST be read-only. A detector runs unprompted against every upstream,
// so a state-changing method must never be added to this list.
var probedMethods = []string{
	"eth_getBlockReceipts",
	"trace_callMany",
	"trace_rawTransaction",
	"eth_simulateV1",
	"eth_getStorageValues",
	"debug_storageRangeAt",
	"eth_getTdByNumber",
	"eth_callBundle",
}

// EvmMethodsDetector runs module attribution first and then probes only what module
// attribution could not settle.
type EvmMethodsDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	base            mapset.Set[string]
}

func NewEvmMethodsDetector(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
	base mapset.Set[string],
) *EvmMethodsDetector {
	return &EvmMethodsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
		base:            base,
	}
}

func (e *EvmMethodsDetector) DetectUnsupported(ctx context.Context) mapset.Set[string] {
	// Stage 1 opines on every base method, probe list included. A module the node does
	// not report cannot contain any method, and that negative must not be second-guessed
	// by a probe that failed for an unrelated reason.
	unsupported := NewRpcModulesDetector(e.upstreamId, e.chain, e.connector, e.internalTimeout, e.base).
		DetectUnsupported(ctx)

	// Stage 2 probes only the probe-list methods whose module survived stage 1 - exactly
	// the case module granularity cannot settle.
	survivors := mapset.NewThreadUnsafeSet[string]()
	for _, method := range probedMethods {
		if e.base.ContainsOne(method) && !unsupported.ContainsOne(method) {
			survivors.Add(method)
		}
	}
	if survivors.IsEmpty() {
		return unsupported
	}

	probed := NewMethodProbeDetector(e.upstreamId, e.chain, e.connector, e.internalTimeout, survivors).
		DetectUnsupported(ctx)

	return unsupported.Union(probed)
}

var _ methods.MethodsDetector = (*EvmMethodsDetector)(nil)
