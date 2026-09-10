package cosmos_validations

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

// CosmosGrpcChainValidator is the gRPC twin of CosmosChainValidator - the
// same chain-id verdicts read from
// cosmos.base.tendermint.v1beta1.Service/GetNodeInfo.
type CosmosGrpcChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewCosmosGrpcChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *CosmosGrpcChainValidator {
	return &CosmosGrpcChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CosmosGrpcChainValidator) Validate() validations.ValidationSettingResult {
	expected := strings.TrimSpace(c.chain.ChainId)
	if expected == "" {
		return validations.Valid
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	nodeInfo, err := specific_helpers.FetchCosmosGrpcNodeInfo(ctx, c.connector, c.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the cosmos grpc node info for upstream '%s'", c.upstreamId)
		return validations.SettingsError
	}
	network := nodeInfo.GetDefaultNodeInfo().GetNetwork()
	if strings.EqualFold(network, expected) {
		return validations.Valid
	}

	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' but cosmos upstream '%s' reports network '%s'",
		c.chain.Chain.String(), expected, c.upstreamId, network,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*CosmosGrpcChainValidator)(nil)
