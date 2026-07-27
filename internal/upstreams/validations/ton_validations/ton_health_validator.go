package ton_validations

import (
	"errors"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errTonNoSeqno = errors.New("ton node returned zero masterchain seqno")

type TonHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewTonHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *TonHealthValidator {
	return &TonHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonHealthValidator) Validate() protocol.AvailabilityStatus {
	info, err := fetchMasterchainInfo(t.connector, t.chain.Chain, t.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("ton upstream '%s' health validation failed", t.upstreamId)
		return protocol.Unavailable
	}
	if info.Result.Last.Seqno == 0 {
		log.Error().Err(errTonNoSeqno).Msgf("ton upstream '%s' has no masterchain head", t.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*TonHealthValidator)(nil)
