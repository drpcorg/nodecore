package celestia_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type CelestiaChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewCelestiaChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *CelestiaChainValidator {
	return &CelestiaChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (c *CelestiaChainValidator) Validate() validations.ValidationSettingResult {
	chainId, err := c.getChainId()
	if err != nil {
		log.Error().Err(err).Msgf("failed to get chainId of chain %s upstream '%s'", c.chain.Chain, c.upstreamId)
		return validations.SettingsError
	}
	if !strings.EqualFold(chainId, c.chain.ChainId) {
		log.Error().Msgf(
			"'%s' is specified for upstream '%s' with chainId '%s', but the node reports chainId '%s'",
			c.chain.Chain.String(),
			c.upstreamId,
			c.chain.ChainId,
			chainId,
		)
		return validations.FatalSettingError
	}
	return validations.Valid
}

func (c *CelestiaChainValidator) getChainId() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"header.LocalHead", []interface{}{}, c.chain.Chain,
	)
	if err != nil {
		return "", err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}

	header, err := specific_helpers.ParseCelestiaExtendedHeader(response.ResponseResult())
	if err != nil {
		return "", err
	}
	return header.Header.ChainId, nil
}

var _ validations.SettingsValidator = (*CelestiaChainValidator)(nil)
