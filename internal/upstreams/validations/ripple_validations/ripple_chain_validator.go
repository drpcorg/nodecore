package ripple_validations

import (
	"strconv"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type RippleChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewRippleChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *RippleChainValidator {
	return &RippleChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (r *RippleChainValidator) Validate() validations.ValidationSettingResult {
	state, err := FetchRippleServerState(r.connector, r.chain.Chain, r.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch server_state of ripple upstream '%s'", r.upstreamId)
		return validations.SettingsError
	}
	// network_id is emitted only when configured; mainnet omits it (absent means 0)
	var networkId uint64
	if state.NetworkId != nil {
		networkId = *state.NetworkId
	}
	// for ripple chains.yaml holds numeric network ids: 0 mainnet / 1 testnet
	if strconv.FormatUint(networkId, 10) == r.chain.ChainId {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects network_id '%s' but ripple upstream '%s' reports '%d'",
		r.chain.Chain.String(),
		r.chain.ChainId,
		r.upstreamId,
		networkId,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*RippleChainValidator)(nil)
