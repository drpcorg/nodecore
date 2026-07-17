package starknet_validations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errStarknetEmptyChainId = errors.New("starknet node returned empty chain id")

type StarknetChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStarknetChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StarknetChainValidator {
	return &StarknetChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StarknetChainValidator) Validate() validations.ValidationSettingResult {
	chainId, err := s.fetchChainId()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch starknet chain id for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	// chains.yaml holds hex-felt chain-ids: 0x534e5f4d41494e (SN_MAIN) / 0x534e5f5345504f4c4941 (SN_SEPOLIA)
	if strings.EqualFold(chainId, s.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects chain id '%s' but starknet upstream '%s' reports '%s'",
		s.chain.Chain.String(),
		s.chain.ChainId,
		s.upstreamId,
		chainId,
	)
	return validations.FatalSettingError
}

func (s *StarknetChainValidator) fetchChainId() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("starknet_chainId", []any{}, s.chain.Chain)
	if err != nil {
		return "", err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	chainId := protocol.ResultAsString(response.ResponseResult())
	if chainId == "" {
		return "", errStarknetEmptyChainId
	}
	return chainId, nil
}

var _ validations.SettingsValidator = (*StarknetChainValidator)(nil)
