package evm_methods

import (
	"context"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// probedMethods are the methods whose module prefix does not settle the question: a node
// can report the containing module as present and still not implement them, so each is
// confirmed with a direct call rather than by module attribution.
//
// Everything here MUST be read-only. A detector runs unprompted against every upstream, so
// a state-changing method must never be added to this list.
var probedMethods = []string{
	"eth_getBlockReceipts",
	"trace_callMany",
	"trace_rawTransaction",
	"eth_simulateV1",
	"eth_getStorageValues",
	"debug_storageRangeAt",
	"eth_getTdByNumber",
	"eth_callBundle",
	"debug_proofsSyncStatus",
}

// MethodProbeDetector calls each method it knows about and keeps the ones the node answers
// for. Params are deliberately empty: a complaint about arguments is itself proof that the
// method exists, so there is nothing to gain from constructing a valid call per method.
type MethodProbeDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	probes          mapset.Set[string]

	// known is the last conclusive answer per method. Retaining per probe rather than per
	// round is what stops one timed-out call from discarding what is known about the
	// others: a round where three probes answer and five time out would otherwise report
	// only the three and silently restore whatever the five had established.
	//
	// knownMu guards it because this detector outlives a single processor run. Rounds within
	// one run cannot overlap, but the instance is reused across PartialStop()/Resume(), and
	// GenericLifecycle.Stop() only cancels the context without waiting for the round in
	// flight - cancelling aborts the probe calls, yet the merge below still runs - while
	// Start() spawns the next round immediately. Two goroutines writing a plain map is a
	// fatal throw rather than a recoverable panic, so the lock is worth its cost of once
	// per round.
	knownMu sync.Mutex
	known   map[string]protocol.MethodAvailability
}

// NewMethodProbeDetector builds a detector for the probe-list methods the chain's spec
// actually declares - a probe naming a method absent from base would only ever be answered
// with "not available", which says nothing about the node.
func NewMethodProbeDetector(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
	base mapset.Set[string],
) *MethodProbeDetector {
	probes := mapset.NewThreadUnsafeSet[string]()
	for _, method := range probedMethods {
		if base.ContainsOne(method) {
			probes.Add(method)
		}
	}

	return &MethodProbeDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
		probes:          probes,
		known:           make(map[string]protocol.MethodAvailability, probes.Cardinality()),
	}
}

func (m *MethodProbeDetector) DetectUnsupported(ctx context.Context) mapset.Set[string] {
	probes := m.probes.ToSlice()

	availability := make([]protocol.MethodAvailability, len(probes))
	var wg sync.WaitGroup
	for index, method := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			availability[index] = m.probe(ctx, method)
		}()
	}
	wg.Wait()

	m.knownMu.Lock()
	defer m.knownMu.Unlock()

	// Merge only conclusive answers, so an inconclusive probe leaves the previous answer
	// for that method in place.
	for index, method := range probes {
		if availability[index] == protocol.MethodAvailabilityUnknown {
			continue
		}
		if availability[index] == protocol.MethodNotAvailable && m.known[method] != protocol.MethodNotAvailable {
			log.Warn().Msgf("method %s is not available on upstream '%s'", method, m.upstreamId)
		}
		m.known[method] = availability[index]
	}

	if len(m.known) == 0 {
		// No probe has ever produced a definite answer.
		return nil
	}

	unsupported := mapset.NewThreadUnsafeSet[string]()
	for method, state := range m.known {
		if state == protocol.MethodNotAvailable {
			unsupported.Add(method)
		}
	}

	return unsupported
}

// probe calls the method with empty params and reports what the answer says about its
// existence. A successful response proves the method is there; an error is classified, and
// anything short of a definite "not available" leaves the method alone.
func (m *MethodProbeDetector) probe(ctx context.Context, method string) protocol.MethodAvailability {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, nil, m.chain)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't create a %s probe request to check method availability of '%s'", method, m.upstreamId)
		return protocol.MethodAvailabilityUnknown
	}

	requestCtx, cancel := context.WithTimeout(ctx, m.internalTimeout)
	defer cancel()

	response := m.connector.SendRequest(requestCtx, request)
	if !response.HasError() {
		return protocol.MethodAvailable
	}

	return protocol.ClassifyMethodAvailability(response.GetError())
}

var _ methods.MethodsDetector = (*MethodProbeDetector)(nil)
