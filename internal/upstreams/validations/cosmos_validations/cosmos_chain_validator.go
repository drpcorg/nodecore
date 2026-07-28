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

type CosmosChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewCosmosChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *CosmosChainValidator {
	return &CosmosChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CosmosChainValidator) Validate() validations.ValidationSettingResult {
	expected := strings.TrimSpace(c.chain.ChainId)
	if expected == "" {
		return validations.Valid
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	nodeInfo, err := specific_helpers.FetchCosmosNodeInfo(ctx, c.connector, c.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the cosmos node_info for upstream '%s'", c.upstreamId)
		return validations.SettingsError
	}
	if strings.EqualFold(nodeInfo.DefaultNodeInfo.Network, expected) {
		return validations.Valid
	}

	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' but cosmos upstream '%s' reports network '%s'",
		c.chain.Chain.String(), expected, c.upstreamId, nodeInfo.DefaultNodeInfo.Network,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*CosmosChainValidator)(nil)
