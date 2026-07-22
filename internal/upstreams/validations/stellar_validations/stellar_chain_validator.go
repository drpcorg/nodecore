package stellar_validations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errStellarEmptyPassphrase = errors.New("stellar node returned empty passphrase")

type StellarChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStellarChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StellarChainValidator {
	return &StellarChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StellarChainValidator) Validate() validations.ValidationSettingResult {
	network, err := s.fetchStellarNetwork()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch stellar network for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	if network.Passphrase == "" {
		// no passphrase means we can't tell what network the node is on - unusable as configured
		log.Error().Err(errStellarEmptyPassphrase).Msgf("failed to validate the chain of stellar upstream '%s'", s.upstreamId)
		return validations.FatalSettingError
	}
	// for stellar chains.yaml holds network-passphrase chain-ids; the registry loader
	// lowercases every chain-id, so the compare is case-insensitive by necessity
	if strings.EqualFold(network.Passphrase, s.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects passphrase '%s' but stellar upstream '%s' reports '%s'",
		s.chain.Chain.String(),
		s.chain.ChainId,
		s.upstreamId,
		network.Passphrase,
	)
	return validations.FatalSettingError
}

func (s *StellarChainValidator) fetchStellarNetwork() (*stellarNetwork, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getNetwork", map[string]any{}, s.chain.Chain)
	if err != nil {
		return nil, err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var network stellarNetwork
	if err := sonic.Unmarshal(response.ResponseResult(), &network); err != nil {
		return nil, err
	}
	return &network, nil
}

type stellarNetwork struct {
	Passphrase string `json:"passphrase"`
}

var _ validations.SettingsValidator = (*StellarChainValidator)(nil)
