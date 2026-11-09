package celestia_validations

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

var errCelestiaNotReady = errors.New("celestia node is not ready")

type CelestiaHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewCelestiaHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *CelestiaHealthValidator {
	return &CelestiaHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CelestiaHealthValidator) Validate(ctx context.Context) protocol.AvailabilityStatus {
	if err := c.checkReady(ctx); err != nil {
		log.Error().Err(err).Msgf("celestia upstream '%s' health validation failed", c.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

func (c *CelestiaHealthValidator) checkReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"node.Ready", []interface{}{}, c.chain,
	)
	if err != nil {
		return err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return response.GetError()
	}

	var ready bool
	if err := sonic.Unmarshal(response.ResponseResult(), &ready); err != nil {
		return err
	}
	if !ready {
		return errCelestiaNotReady
	}
	return nil
}

var _ validations.HealthValidator = (*CelestiaHealthValidator)(nil)
