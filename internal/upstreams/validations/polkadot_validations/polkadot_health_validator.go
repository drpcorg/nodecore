package polkadot_validations

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// polkadotHealth is the system_health payload.
type polkadotHealth struct {
	Peers           int64 `json:"peers"`
	IsSyncing       bool  `json:"isSyncing"`
	ShouldHavePeers bool  `json:"shouldHavePeers"`
}

// PolkadotHealthValidator derives both the syncing and the peer-count verdict
// from a single system_health call. Splitting these into two validators the way
// tendermint_validations does would double the probe traffic, because substrate
// returns both signals in the same response.
type PolkadotHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
	validateSyncing bool
	validatePeers   bool
	minPeers        int64
}

func NewPolkadotHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
	validateSyncing bool,
	validatePeers bool,
	minPeers int64,
) *PolkadotHealthValidator {
	return &PolkadotHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
		validateSyncing: validateSyncing,
		validatePeers:   validatePeers,
		minPeers:        minPeers,
	}
}

func (p *PolkadotHealthValidator) Validate() protocol.AvailabilityStatus {
	health, err := p.fetchHealth()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get system_health of upstream '%s'", p.upstreamId)
		return protocol.Unavailable
	}
	if p.validateSyncing && health.IsSyncing {
		log.Warn().Msgf("polkadot upstream '%s' is in syncing state", p.upstreamId)
		return protocol.Syncing
	}
	// shouldHavePeers is the node's own statement that it expects peers; an
	// intentionally isolated node (light client, --dev) reports false and must not
	// be penalised for having none.
	if p.validatePeers && health.ShouldHavePeers && health.Peers < p.minPeers {
		log.Warn().Msgf(
			"polkadot upstream '%s' should but doesn't have enough peers (%d < %d)",
			p.upstreamId, health.Peers, p.minPeers,
		)
		return protocol.Immature
	}
	return protocol.Available
}

func (p *PolkadotHealthValidator) fetchHealth() (*polkadotHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("system_health", []any{}, p.chain)
	if err != nil {
		return nil, err
	}
	response := p.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var health polkadotHealth
	if err := sonic.Unmarshal(response.ResponseResult(), &health); err != nil {
		return nil, fmt.Errorf("polkadot system_health payload unparseable: %w", err)
	}
	return &health, nil
}

var _ validations.HealthValidator = (*PolkadotHealthValidator)(nil)
