package beacon_validations

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// BeaconChainHealthValidator probes the beacon liveness endpoint
// GET /eth/v1/node/health, which answers 200 (ready), 206 (syncing) or 503 (not
// initialized). Any non-2xx status is surfaced by the REST connector as an error,
// so the upstream is Available on a successful response and Unavailable otherwise.
type BeaconChainHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewBeaconChainHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *BeaconChainHealthValidator {
	return &BeaconChainHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (b *BeaconChainHealthValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/node/health", nil, b.chain)

	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		log.Error().Err(response.GetError()).Msgf("beacon upstream '%s' health validation failed", b.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*BeaconChainHealthValidator)(nil)
