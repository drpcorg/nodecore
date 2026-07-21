package near_validations

import (
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errNearEmptyChainId = errors.New("near node returned empty chain_id")

type NearChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewNearChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *NearChainValidator {
	return &NearChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (n *NearChainValidator) Validate() validations.ValidationSettingResult {
	status, err := fetchNearStatus(n.connector, n.chain.Chain, n.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch near status for upstream '%s'", n.upstreamId)
		return validations.SettingsError
	}
	if status.ChainId == "" {
		// no chain_id means we can't tell what network the node is on - unusable as configured
		log.Error().Err(errNearEmptyChainId).Msgf("failed to validate the chain of near upstream '%s'", n.upstreamId)
		return validations.FatalSettingError
	}
	// for near chains.yaml holds network-name chain-ids: mainnet/testnet/betanet
	if strings.EqualFold(status.ChainId, n.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects chain_id '%s' but near upstream '%s' reports '%s'",
		n.chain.Chain.String(),
		n.chain.ChainId,
		n.upstreamId,
		status.ChainId,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*NearChainValidator)(nil)
