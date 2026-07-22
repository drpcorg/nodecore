package stellar_validations

import (
	"context"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const stellarHealthyStatus = "healthy"

// stellar-rpc rejects getHealth with a JSON-RPC error containing this text
// while its data stores are still bootstrapping
const stellarNotInitializedMarker = "not initialized"

type StellarSyncingValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStellarSyncingValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StellarSyncingValidator {
	return &StellarSyncingValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarSyncingValidator) Validate() protocol.AvailabilityStatus {
	health, err := FetchStellarHealth(s.connector, s.chain.Chain, s.internalTimeout)
	if err != nil {
		if strings.Contains(err.Error(), stellarNotInitializedMarker) {
			log.Warn().Msgf("stellar upstream '%s' is bootstrapping its data stores", s.upstreamId)
			return protocol.Syncing
		}
		// incl. the node's own staleness rejection ("latency ... too high") and transport errors
		log.Error().Err(err).Msgf("stellar upstream '%s' syncing validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	if health.Status != stellarHealthyStatus {
		log.Warn().Msgf("stellar upstream '%s' reports status '%s'", s.upstreamId, health.Status)
		return protocol.Unavailable
	}
	return protocol.Available
}

func FetchStellarHealth(connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*StellarHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getHealth", map[string]any{}, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var health StellarHealth
	if err := sonic.Unmarshal(response.ResponseResult(), &health); err != nil {
		return nil, err
	}
	return &health, nil
}

type StellarHealth struct {
	Status                string `json:"status"`
	LatestLedger          uint64 `json:"latestLedger"`
	OldestLedger          uint64 `json:"oldestLedger"`
	LatestLedgerCloseTime string `json:"latestLedgerCloseTime"`
	OldestLedgerCloseTime string `json:"oldestLedgerCloseTime"`
	LedgerRetentionWindow uint64 `json:"ledgerRetentionWindow"`
}

var _ validations.HealthValidator = (*StellarSyncingValidator)(nil)
