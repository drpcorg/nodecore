package stellar_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type StellarHorizonChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStellarHorizonChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StellarHorizonChainValidator {
	return &StellarHorizonChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarHorizonChainValidator) Validate() validations.ValidationSettingResult {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	root, err := specific_helpers.FetchStellarHorizonRoot(ctx, s.connector, s.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the horizon root document for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	// chains.yaml holds network-passphrase chain-ids and the registry loader
	// lowercases every chain-id, so the compare is case-insensitive by necessity.
	// An empty passphrase fails this compare too - the chain-id is never empty
	// here, since SettingsValidators() skips the validator entirely in that case.
	if strings.EqualFold(root.NetworkPassphrase, s.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects passphrase '%s' but horizon upstream '%s' reports '%s'",
		s.chain.Chain.String(),
		s.chain.ChainId,
		s.upstreamId,
		root.NetworkPassphrase,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*StellarHorizonChainValidator)(nil)
