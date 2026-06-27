package aptos_validations

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// healthDurationSecs is the window passed to /v1/-/healthy: the node reports
// healthy only if it has committed a transaction within this many seconds.
const healthDurationSecs = "10"

type AptosHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewAptosHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *AptosHealthValidator {
	return &AptosHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AptosHealthValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest(
		"GET#/v1/-/healthy",
		&protocol.RequestParams{QueryParams: map[string][]string{"duration_secs": {healthDurationSecs}}},
		a.chain,
	)
	response := a.connector.SendRequest(ctx, request)

	if response.ResponseCode() == 503 {
		log.Warn().Msgf("aptos upstream '%s' is not ready (503)", a.upstreamId)
		return protocol.Syncing
	}
	if response.HasError() {
		log.Error().Err(response.GetError()).Msgf("aptos upstream '%s' health check failed", a.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*AptosHealthValidator)(nil)
