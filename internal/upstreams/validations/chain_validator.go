package validations

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

type ChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.ConfiguredChain,
	options *config.UpstreamOptions,
) *ChainValidator {
	return &ChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: options.InternalTimeout,
	}
}

func (c *ChainValidator) Validate() ValidationSettingResult {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	group, ctx := errgroup.WithContext(ctx)
	logger := zerolog.Ctx(ctx)

	var chainId, netVersion string

	group.Go(func() error {
		result, err := c.getChainId(ctx)
		if err != nil {
			logger.Warn().Err(err).Msgf("failed to get chainId")
			return err
		}
		chainId = result
		return nil
	})

	group.Go(func() error {
		result, err := c.getNetVersion(ctx)
		if err != nil {
			logger.Warn().Err(err).Msgf("failed to get netVersion")
			return err
		}
		netVersion = result
		return nil
	})

	if err := group.Wait(); err != nil {
		return SettingsError
	}

	isValidChain := c.chain.ChainId == chainId && c.chain.NetVersion == netVersion
	if !isValidChain {
		actualChain := chains.GetChainByChainIdAndVersion(chainId, netVersion)
		log.Error().Msgf(
			"'%s' is specified for upstream '%s' with chainId '%s' and netVersion '%s', but actually it's '%s' with chainId '%s' and netVersion '%s'",
			c.chain.Chain.String(),
			c.upstreamId,
			c.chain.ChainId,
			c.chain.NetVersion,
			actualChain.Chain.String(),
			chainId,
			netVersion,
		)

		return FatalSettingError
	}

	return Valid
}

func (c *ChainValidator) getChainId(ctx context.Context) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	if err != nil {
		return "", err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}

	return response.ResponseResultString()
}

func (c *ChainValidator) getNetVersion(ctx context.Context) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)
	if err != nil {
		return "", err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}

	return response.ResponseResultString()
}

var _ SettingsValidator = (*ChainValidator)(nil)
