package ripple_validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var (
	errRippleAmendmentBlocked = errors.New("rippled node is amendment blocked")
	errRippleNoPeers          = errors.New("rippled node has no peers")
)

type RippleHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	validatePeers   bool
}

func NewRippleHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	validatePeers bool,
) *RippleHealthValidator {
	return &RippleHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
		validatePeers:   validatePeers,
	}
}

func (r *RippleHealthValidator) Validate() protocol.AvailabilityStatus {
	state, err := FetchRippleServerState(r.connector, r.chain.Chain, r.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("ripple upstream '%s' health validation failed", r.upstreamId)
		return protocol.Unavailable
	}
	// the field is emitted only when the node is blocked; a blocked node is
	// stuck until a binary upgrade, whatever server_state it reports
	if state.AmendmentBlocked != nil && *state.AmendmentBlocked {
		log.Error().Err(errRippleAmendmentBlocked).Msgf("ripple upstream '%s' is amendment blocked", r.upstreamId)
		return protocol.Unavailable
	}
	status := availabilityByServerState(state.ServerState)
	switch status {
	case protocol.Syncing:
		log.Warn().Msgf("ripple upstream '%s' is syncing, server_state=%s", r.upstreamId, state.ServerState)
	case protocol.Unavailable:
		log.Error().Msgf("ripple upstream '%s' is unavailable, server_state='%s'", r.upstreamId, state.ServerState)
	}
	// peers come from the same server_state payload — no extra call
	if r.validatePeers && state.Peers == 0 {
		log.Error().Err(errRippleNoPeers).Msgf("ripple upstream '%s' has no peers", r.upstreamId)
		return protocol.Unavailable
	}
	return status
}

// mapping deliberately differs from dshackle: 'tracking' is in agreement with
// the network and serving-ready, 'syncing' is transient rather than unavailable
func availabilityByServerState(serverState string) protocol.AvailabilityStatus {
	switch serverState {
	case "full", "validating", "proposing", "tracking":
		return protocol.Available
	case "connected", "syncing":
		return protocol.Syncing
	default:
		return protocol.Unavailable
	}
}

// FetchRippleServerState requests 'server_state' and returns the inner
// result.state object. rippled requires params to be a one-object array
// ([{}]); a plain empty array is rejected with an HTTP 400.
func FetchRippleServerState(connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*RippleServerState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("server_state", []any{map[string]any{}}, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var result rippleServerStateResult
	if err := sonic.Unmarshal(response.ResponseResult(), &result); err != nil {
		return nil, err
	}
	return &result.State, nil
}

type rippleServerStateResult struct {
	State RippleServerState `json:"state"`
}

type RippleServerState struct {
	ServerState      string                `json:"server_state"`
	NetworkId        *uint64               `json:"network_id"` // absent on mainnet (absent means 0)
	CompleteLedgers  string                `json:"complete_ledgers"`
	BuildVersion     string                `json:"build_version"`
	Peers            int                   `json:"peers"`
	AmendmentBlocked *bool                 `json:"amendment_blocked"` // present only when blocked
	ValidatedLedger  RippleValidatedLedger `json:"validated_ledger"`
}

type RippleValidatedLedger struct {
	Seq  uint64 `json:"seq"`
	Hash string `json:"hash"`
}

var _ validations.HealthValidator = (*RippleHealthValidator)(nil)
