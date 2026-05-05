package validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var (
	errAlgorandStillCatchingUp = errors.New("algorand node is still catching up")
	errAlgorandStoppedUpgrade  = errors.New("algorand node stopped at an unsupported round")
	errAlgorandNoRound         = errors.New("algorand node returned no last-round")
)

type AlgorandHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewAlgorandHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *AlgorandHealthValidator {
	return &AlgorandHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AlgorandHealthValidator) Validate() protocol.AvailabilityStatus {
	status, err := a.fetchStatus()
	if err != nil {
		log.Error().Err(err).Msgf("algorand upstream '%s' health validation failed", a.upstreamId)
		return protocol.Unavailable
	}
	if status.LastRound == 0 {
		log.Error().Err(errAlgorandNoRound).Msgf("algorand upstream '%s' has no last-round", a.upstreamId)
		return protocol.Unavailable
	}
	if status.StoppedAtUnsupportedRound {
		log.Error().Err(errAlgorandStoppedUpgrade).Msgf("algorand upstream '%s' halted on consensus upgrade", a.upstreamId)
		return protocol.Unavailable
	}
	if status.CatchupTime > 0 {
		log.Warn().Err(errAlgorandStillCatchingUp).Msgf("algorand upstream '%s' is catching up", a.upstreamId)
		return protocol.Syncing
	}
	return protocol.Available
}

func (a *AlgorandHealthValidator) fetchStatus() (*AlgorandStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET", "/v2/status", a.chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var status AlgorandStatus
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

type AlgorandStatus struct {
	LastRound                 uint64 `json:"last-round"`
	CatchupTime               int64  `json:"catchup-time"`
	StoppedAtUnsupportedRound bool   `json:"stopped-at-unsupported-round"`
}

var _ HealthValidator = (*AlgorandHealthValidator)(nil)
