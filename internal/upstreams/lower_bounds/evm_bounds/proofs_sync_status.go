package evm_bounds

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	evmProofsSyncStatusMethod = "debug_proofsSyncStatus"

	// An upstream without the method is re-probed occasionally: nodes get upgraded.
	evmProofsSyncStatusReprobeInterval = time.Hour
)

// EvmProofsSyncStatus asks an op-reth upstream for the block window its historical proof
// store serves (debug_proofsSyncStatus -> {"earliest","latest"}). It is the first source
// the proof detector consults: one call replaces the eth_getProof binary search for the
// lower proof bound and is the only source of the upper proof bound. An upstream that
// rejects the method as unknown, or answers with an unparseable body, is remembered as
// unsupported and re-asked hourly; transient failures leave the verdict alone.
type EvmProofsSyncStatus struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	connector       connectors.ApiConnector
	reprobeInterval time.Duration

	mu            sync.Mutex
	unsupported   bool
	lastAttemptAt time.Time
}

func NewEvmProofsSyncStatus(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmProofsSyncStatus {
	return &EvmProofsSyncStatus{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
		reprobeInterval: evmProofsSyncStatusReprobeInterval,
	}
}

// SetReprobeInterval overrides how often an unsupported upstream is re-asked. Production
// relies on the default; tests shrink it.
func (s *EvmProofsSyncStatus) SetReprobeInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reprobeInterval = interval
}

// evmProofWindow is the inclusive block range served from the proof store.
type evmProofWindow struct {
	earliest int64
	latest   int64
}

// window returns the upstream's current proof window, or nil when there is no usable one:
// method unsupported (cached verdict), transient failure, malformed body, or a store that
// is still empty (latest 0 or earliest > latest).
func (s *EvmProofsSyncStatus) window(ctx context.Context) *evmProofWindow {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.unsupported && time.Since(s.lastAttemptAt) < s.reprobeInterval {
		return nil
	}
	s.lastAttemptAt = time.Now()

	response, err := s.send(ctx)
	if err != nil {
		log.Debug().Err(err).Msgf("couldn't request %s from upstream '%s'", evmProofsSyncStatusMethod, s.upstreamId)
		return nil
	}
	if response.HasError() {
		respErr := response.GetError()
		if isEvmMethodNotFoundError(respErr) {
			s.markUnsupported(respErr.Message)
			return nil
		}
		log.Debug().Err(respErr).Msgf("couldn't fetch %s from upstream '%s'", evmProofsSyncStatusMethod, s.upstreamId)
		return nil
	}

	window, err := parseEvmProofsSyncStatus(response.ResponseResult())
	if err != nil {
		s.markUnsupported(err.Error())
		return nil
	}
	if s.unsupported {
		log.Info().Msgf("upstream '%s' supports %s, using it for proof bound detection", s.upstreamId, evmProofsSyncStatusMethod)
	}
	s.unsupported = false
	if window.latest == 0 || window.earliest > window.latest {
		log.Debug().Msgf("upstream '%s' reports an empty proof window [%d, %d]", s.upstreamId, window.earliest, window.latest)
		return nil
	}
	return window
}

func (s *EvmProofsSyncStatus) send(ctx context.Context) (protocol.ResponseHolder, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(evmProofsSyncStatusMethod, []any{}, s.chain.Chain)
	if err != nil {
		return nil, err
	}
	return s.connector.SendRequest(ctx, request), nil
}

func (s *EvmProofsSyncStatus) markUnsupported(cause string) {
	if !s.unsupported {
		log.Debug().Msgf("upstream '%s' doesn't support %s (%s), falling back to eth_capabilities/search", s.upstreamId, evmProofsSyncStatusMethod, cause)
	}
	s.unsupported = true
}

type evmProofsSyncStatusResponse struct {
	Earliest json.RawMessage `json:"earliest"`
	Latest   json.RawMessage `json:"latest"`
}

// parseEvmProofsSyncStatus maps the raw result to a window. A missing or unparseable
// field is malformed. earliest 0x0 is coerced to 1: nodecore's convention for "from the
// first block" is bound 1, and a 0 prediction reads as "unknown" to routing.
func parseEvmProofsSyncStatus(raw []byte) (*evmProofWindow, error) {
	if isEvmNullResult(raw) {
		return nil, fmt.Errorf("null result")
	}
	parsed := evmProofsSyncStatusResponse{}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	if len(parsed.Earliest) == 0 || len(parsed.Latest) == 0 {
		return nil, fmt.Errorf("malformed response: earliest or latest missing")
	}
	earliest, err := parseEvmBlockNumber(parsed.Earliest)
	if err != nil || earliest < 0 {
		return nil, fmt.Errorf("malformed earliest: %w", err)
	}
	latest, err := parseEvmBlockNumber(parsed.Latest)
	if err != nil || latest < 0 {
		return nil, fmt.Errorf("malformed latest: %w", err)
	}
	if earliest == 0 {
		earliest = 1
	}
	return &evmProofWindow{earliest: earliest, latest: latest}, nil
}

// detectFromProofsSyncStatus resolves the proof window straight from the upstream's
// historical proof store. It emits ProofBound (earliest) and UpperProofBound (latest).
// UpperProofBound is deliberately absent from SupportedTypes: the capabilities and search
// paths cannot produce it, and SupportedTypes drives their fan-out.
func (e *EvmLowerBoundDetector) detectFromProofsSyncStatus(ctx context.Context) ([]protocol.LowerBoundData, bool) {
	if e.proofsSyncStatus == nil || e.MainBoundType != protocol.ProofBound {
		return nil, false
	}
	window := e.proofsSyncStatus.window(ctx)
	if window == nil {
		return nil, false
	}
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(window.earliest, protocol.ProofBound),
		protocol.NewLowerBoundDataNow(window.latest, protocol.UpperProofBound),
	}, true
}
