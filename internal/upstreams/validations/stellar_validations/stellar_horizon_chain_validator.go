package stellar_validations

import (
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errStellarHorizonEmptyPassphrase = errors.New("horizon returned empty network_passphrase")

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
	root, err := FetchStellarHorizonRoot(s.connector, s.chain.Chain, s.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the horizon root document for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	if root.NetworkPassphrase == "" {
		// no passphrase means we can't tell what network the node is on - unusable as configured
		log.Error().Err(errStellarHorizonEmptyPassphrase).Msgf("failed to validate the chain of horizon upstream '%s'", s.upstreamId)
		return validations.FatalSettingError
	}
	// for stellar chains.yaml holds network-passphrase chain-ids; the registry loader
	// lowercases every chain-id, so the compare is case-insensitive by necessity
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
