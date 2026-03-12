package validations

import (
	"context"
	"errors"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/rs/zerolog/log"
)

type SolanaHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (s *SolanaHealthValidator) Validate() protocol.AvailabilityStatus {
	err := s.getHealth()
	if err != nil {
		log.Warn().Err(err).Msgf("solana upstream '%s' health validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

var i = 0

func (s *SolanaHealthValidator) getHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getHealth", nil)
	if err != nil {
		return err
	}

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return response.GetError()
	}

	result, err := response.ResponseResultString()
	if err != nil {
		return err
	}
	i++
	if s.upstreamId == "upstream-231" && i > 2 && i <= 5 {
		return errors.New("solana upstream health check failed")
	}

	if result == "ok" {
		return nil
	}
	return errors.New("status is not ok")
}

func NewSolanaHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *SolanaHealthValidator {
	return &SolanaHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

var _ HealthValidator = (*SolanaHealthValidator)(nil)
