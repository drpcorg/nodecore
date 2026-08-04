package polkadot_validations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errPolkadotEmptyChainName = errors.New("polkadot node returned an empty chain name")

// PolkadotChainValidator compares system_chain against the configured chain-id
// ("Polkadot", "Kusama", "Vara Network", ...), so a node pointed at the wrong
// network is rejected instead of silently serving another chain's data.
type PolkadotChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewPolkadotChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *PolkadotChainValidator {
	return &PolkadotChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (p *PolkadotChainValidator) Validate() validations.ValidationSettingResult {
	chainName, err := p.fetchChainName()
	if err != nil {
		if errors.Is(err, errPolkadotEmptyChainName) {
			// No chain name means we cannot tell what network this is - unusable as configured.
			log.Error().Err(err).Msgf("failed to validate the chain of polkadot upstream '%s'", p.upstreamId)
			return validations.FatalSettingError
		}
		log.Error().Err(err).Msgf("failed to fetch the chain name of polkadot upstream '%s'", p.upstreamId)
		return validations.SettingsError
	}
	if strings.EqualFold(chainName, p.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects chain '%s' but polkadot upstream '%s' reports '%s'",
		p.chain.Chain.String(),
		p.chain.ChainId,
		p.upstreamId,
		chainName,
	)
	return validations.FatalSettingError
}

func (p *PolkadotChainValidator) fetchChainName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("system_chain", []any{}, p.chain.Chain)
	if err != nil {
		return "", err
	}
	response := p.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	result := response.ResponseResult()
	if specific_helpers.IsJsonNull(result) {
		return "", errPolkadotEmptyChainName
	}
	chainName := protocol.ResultAsString(result)
	if chainName == "" {
		return "", errPolkadotEmptyChainName
	}
	return chainName, nil
}

var _ validations.SettingsValidator = (*PolkadotChainValidator)(nil)
