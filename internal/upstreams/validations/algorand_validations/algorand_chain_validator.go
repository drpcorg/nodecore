package algorand_validations

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

var errAlgorandEmptyGenesis = errors.New("algorand node returned empty genesis")

type AlgorandChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewAlgorandChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *AlgorandChainValidator {
	return &AlgorandChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AlgorandChainValidator) Validate() validations.ValidationSettingResult {
	expected := strings.TrimSpace(a.chain.ChainId)
	if expected == "" {
		return validations.Valid
	}
	expectedNetwork, hasMapping := algorandChainIdNetwork[strings.ToLower(expected)]
	if !hasMapping {
		log.Error().Msgf(
			"algorand upstream '%s' has unknown chain-id '%s'",
			a.upstreamId,
			expected,
		)
		return validations.FatalSettingError
	}
	genesis, err := a.fetchGenesis()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch algorand genesis for upstream '%s'", a.upstreamId)
		return validations.SettingsError
	}
	if strings.EqualFold(genesis.Network, expectedNetwork) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' (expected network='%s') but algorand upstream '%s' reports network='%s' id='%s'",
		a.chain.Chain.String(),
		expected,
		expectedNetwork,
		a.upstreamId,
		genesis.Network,
		genesis.Id,
	)
	return validations.FatalSettingError
}

var algorandChainIdNetwork = map[string]string{
	"0x65901": "mainnet",
	"0x65902": "testnet",
	"0x65903": "betanet",
}

func (a *AlgorandChainValidator) fetchGenesis() (*algorandGenesis, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/genesis", nil, a.chain.Chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, errAlgorandEmptyGenesis
	}
	var genesis algorandGenesis
	if err := sonic.Unmarshal(raw, &genesis); err != nil {
		return nil, err
	}
	return &genesis, nil
}

type algorandGenesis struct {
	Network string `json:"network"`
	Id      string `json:"id"`
}

var _ validations.SettingsValidator = (*AlgorandChainValidator)(nil)
