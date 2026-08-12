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

// MethodProbeDetector calls each method it is given and keeps the ones the node answers
// for. Params are deliberately empty: a complaint about arguments is itself proof that
// the method exists, so there is nothing to gain from constructing a valid call per
// method. Only a definite "this method is absent" strips anything.
type MethodProbeDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	probes          mapset.Set[string]
}

func NewMethodProbeDetector(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
	probes mapset.Set[string],
) *MethodProbeDetector {
	return &MethodProbeDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
		probes:          probes,
	}
}

func (m *MethodProbeDetector) DetectUnsupported(ctx context.Context) mapset.Set[string] {
	unsupported := mapset.NewThreadUnsafeSet[string]()

	probes := m.probes.ToSlice()
	if len(probes) == 0 {
		return unsupported
	}

	absent := make([]bool, len(probes))
	var wg sync.WaitGroup
	for index, method := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			absent[index] = m.isAbsent(ctx, method)
		}()
	}
	wg.Wait()

	for index, method := range probes {
		if absent[index] {
			unsupported.Add(method)
		}
	}

	return unsupported
}

// isAbsent reports whether the node definitely does not have the method. Anything short
// of a definite answer is false, so the method survives.
func (m *MethodProbeDetector) isAbsent(ctx context.Context, method string) bool {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, nil, m.chain)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't create a %s probe request of '%s'", method, m.upstreamId)
		return false
	}

	requestCtx, cancel := context.WithTimeout(ctx, m.internalTimeout)
	defer cancel()

	response := m.connector.SendRequest(requestCtx, request)
	if !response.HasError() {
		return false
	}

	if protocol.ClassifyMethodAvailability(response.GetError()) == protocol.MethodNotAvailable {
		log.Warn().Msgf("method %s is not available on upstream '%s'", method, m.upstreamId)
		return true
	}

	return false
}

var _ methods.MethodsDetector = (*MethodProbeDetector)(nil)
