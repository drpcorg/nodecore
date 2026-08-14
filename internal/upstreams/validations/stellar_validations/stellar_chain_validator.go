package stellar_validations

import (
	"context"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type stellarNetwork struct {
	Passphrase string `json:"passphrase"`
}

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
	passphrase, err := s.fetchPassphrase()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the stellar network for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	// chains.yaml holds network-passphrase chain-ids and the registry loader
	// lowercases every chain-id, so the compare is case-insensitive by necessity.
	// An empty passphrase fails this compare too - the chain-id is never empty
	// here, since SettingsValidators() skips the validator entirely in that case.
	if strings.EqualFold(passphrase, s.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects passphrase '%s' but stellar upstream '%s' reports '%s'",
		s.chain.Chain.String(),
		s.chain.ChainId,
		s.upstreamId,
		passphrase,
	)
	return validations.FatalSettingError
}

func (s *StellarChainValidator) fetchPassphrase() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getNetwork", map[string]any{}, s.chain.Chain)
	if err != nil {
		return "", err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	var network stellarNetwork
	if err := sonic.Unmarshal(response.ResponseResult(), &network); err != nil {
		return "", err
	}
	return network.Passphrase, nil
}

var _ validations.SettingsValidator = (*StellarChainValidator)(nil)
