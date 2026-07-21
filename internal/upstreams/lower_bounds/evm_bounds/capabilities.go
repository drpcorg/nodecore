package evm_bounds

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	evmCapabilitiesMethod = "eth_capabilities"

	// A fetched report is shared by all detectors of the upstream within one detection
	// cycle; half the cycle period keeps it to a single call per cycle even if the
	// detector loops drift apart.
	evmCapabilitiesResultTtl = evmLowerBoundPeriod / 2

	// An upstream without the method is re-probed occasionally: nodes get upgraded.
	evmCapabilitiesReprobeInterval = time.Hour
)

type evmCapabilitiesSupport int

const (
	evmCapabilitiesSupported evmCapabilitiesSupport = iota
	evmCapabilitiesUnsupported
)

// EvmCapabilities caches the upstream's eth_capabilities report (geth >= 1.17.4), which
// states the oldest served block per data type and replaces the per-type binary searches.
// One instance is shared by all lower-bound detectors of an upstream, so the method is
// called once per result window instead of once per bound type. The values are trusted
// as reported; the probe path stays intact for upstreams without the method.
type EvmCapabilities struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	connector       connectors.ApiConnector

	resultTtl       time.Duration
	reprobeInterval time.Duration

	mu            sync.Mutex
	support       evmCapabilitiesSupport
	lastAttemptAt time.Time
	cached        *evmCapabilitiesSnapshot
}

func NewEvmCapabilities(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmCapabilities {
	return &EvmCapabilities{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
		resultTtl:       evmCapabilitiesResultTtl,
		reprobeInterval: evmCapabilitiesReprobeInterval,
	}
}

// SetProbeWindows overrides how long a fetched report is served from cache and how often
// an unsupported upstream is re-probed. Production relies on the defaults; tests shrink them.
func (c *EvmCapabilities) SetProbeWindows(resultTtl, reprobeInterval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resultTtl = resultTtl
	c.reprobeInterval = reprobeInterval
}

// snapshot returns the current capabilities report or nil when the upstream has no usable
// one (method unsupported, transient failure, malformed response). Within a window the
// cached answer is served without an RPC; only the first detector of a cycle pays the call.
func (c *EvmCapabilities) snapshot(ctx context.Context) *evmCapabilitiesSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	window := c.resultTtl
	if c.support == evmCapabilitiesUnsupported {
		window = c.reprobeInterval
	}
	if time.Since(c.lastAttemptAt) < window {
		return c.cached
	}

	c.lastAttemptAt = time.Now()
	c.cached = c.refresh(ctx)
	return c.cached
}

func (c *EvmCapabilities) refresh(ctx context.Context) *evmCapabilitiesSnapshot {
	response, err := c.send(ctx)
	if err != nil {
		log.Debug().Err(err).Msgf("couldn't request %s from upstream '%s'", evmCapabilitiesMethod, c.upstreamId)
		return nil
	}
	if response.HasError() {
		respErr := response.GetError()
		if isEvmMethodNotFoundError(respErr) {
			c.markUnsupported(respErr.Message)
			return nil
		}
		// a transient upstream failure doesn't change the support verdict
		log.Debug().Err(respErr).Msgf("couldn't fetch %s from upstream '%s'", evmCapabilitiesMethod, c.upstreamId)
		return nil
	}

	snapshot := parseEvmCapabilities(response.ResponseResult())
	if snapshot == nil {
		c.markUnsupported("malformed response")
		return nil
	}
	if c.support != evmCapabilitiesSupported {
		log.Info().Msgf("upstream '%s' supports %s, using it for lower bound detection", c.upstreamId, evmCapabilitiesMethod)
	}
	c.support = evmCapabilitiesSupported
	return snapshot
}

func (c *EvmCapabilities) send(ctx context.Context) (protocol.ResponseHolder, error) {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(evmCapabilitiesMethod, []any{}, c.chain.Chain)
	if err != nil {
		return nil, err
	}
	return c.connector.SendRequest(ctx, request), nil
}

func (c *EvmCapabilities) markUnsupported(cause string) {
	if c.support != evmCapabilitiesUnsupported {
		log.Debug().Msgf("upstream '%s' doesn't support %s (%s), falling back to lower bound search", c.upstreamId, evmCapabilitiesMethod, cause)
	}
	c.support = evmCapabilitiesUnsupported
}

// isEvmMethodNotFoundError reports whether the upstream rejected eth_capabilities because
// it doesn't know the method (older geth, other clients, RPC gateways). Matching errs
// toward "unsupported": a wrong verdict only delays capabilities usage until the re-probe.
func isEvmMethodNotFoundError(err *protocol.ResponseError) bool {
	if err == nil {
		return false
	}
	if err.Code == protocol.NoSupportedMethod {
		return true
	}
	message := strings.ToLower(err.Message)
	for _, hint := range evmMethodNotFoundHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

var evmMethodNotFoundHints = []string{
	"method not found",
	"does not exist",
	"not supported",
	"unsupported method",
	"not enabled",
	"not implemented",
}

type evmCapabilityEntry struct {
	Disabled    bool            `json:"disabled"`
	OldestBlock json.RawMessage `json:"oldestBlock"`
}

// head and deleteStrategy are intentionally ignored for now.
type evmCapabilitiesResponse struct {
	State       *evmCapabilityEntry `json:"state"`
	Tx          *evmCapabilityEntry `json:"tx"`
	Receipts    *evmCapabilityEntry `json:"receipts"`
	Blocks      *evmCapabilityEntry `json:"blocks"`
	Logs        *evmCapabilityEntry `json:"logs"`
	StateProofs *evmCapabilityEntry `json:"stateproofs"`
}

type evmCapabilityResource struct {
	disabled bool
	bound    int64
}

type evmCapabilitiesSnapshot struct {
	resources map[protocol.LowerBoundType]evmCapabilityResource
}

// resource answers for TraceBound with the state capability: nodecore derives the trace
// bound from the state search today, and traces are re-executed from state.
func (s *evmCapabilitiesSnapshot) resource(boundType protocol.LowerBoundType) (evmCapabilityResource, bool) {
	if boundType == protocol.TraceBound {
		boundType = protocol.StateBound
	}
	res, ok := s.resources[boundType]
	return res, ok
}

// parseEvmCapabilities maps a raw eth_capabilities result to per-bound-type resources.
// Unparseable entries are dropped (their detectors fall back to the search); a report
// with no usable entries at all counts as malformed. Geth reports a fully archival
// resource as oldestBlock 0x0 while nodecore's convention for "full data" is bound 1.
func parseEvmCapabilities(raw []byte) *evmCapabilitiesSnapshot {
	if isEvmNullResult(raw) {
		return nil
	}
	parsed := evmCapabilitiesResponse{}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		return nil
	}

	entries := map[protocol.LowerBoundType]*evmCapabilityEntry{
		protocol.StateBound:    parsed.State,
		protocol.TxBound:       parsed.Tx,
		protocol.ReceiptsBound: parsed.Receipts,
		protocol.BlockBound:    parsed.Blocks,
		protocol.LogsBound:     parsed.Logs,
		protocol.ProofBound:    parsed.StateProofs,
	}
	resources := make(map[protocol.LowerBoundType]evmCapabilityResource, len(entries))
	for boundType, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.Disabled {
			resources[boundType] = evmCapabilityResource{disabled: true}
			continue
		}
		bound, err := parseHexInt(entry.OldestBlock)
		if err != nil || bound < 0 {
			continue
		}
		if bound == 0 {
			bound = 1
		}
		resources[boundType] = evmCapabilityResource{bound: bound}
	}
	if len(resources) == 0 {
		return nil
	}
	return &evmCapabilitiesSnapshot{resources: resources}
}

// detectFromCapabilities resolves this detector's bound types straight from the upstream's
// self-reported capabilities. It takes over only when every supported type is covered by
// the report; otherwise the caller falls back to the gold-bound/search path. A disabled
// resource is covered but yields no bound: routing treats an absent bound as "this
// upstream has no data of that type" and excludes it.
func (e *EvmLowerBoundDetector) detectFromCapabilities(ctx context.Context) ([]protocol.LowerBoundData, bool) {
	if e.capabilities == nil {
		return nil, false
	}
	snapshot := e.capabilities.snapshot(ctx)
	if snapshot == nil {
		return nil, false
	}

	results := make([]protocol.LowerBoundData, 0, len(e.SupportedTypes()))
	for _, boundType := range e.SupportedTypes() {
		res, covered := snapshot.resource(boundType)
		if !covered {
			return nil, false
		}
		if res.disabled {
			continue
		}
		results = append(results, protocol.NewLowerBoundDataNow(res.bound, boundType))
	}
	return results, true
}
