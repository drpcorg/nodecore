package eth_validations

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

type EthChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration

	executor failsafe.Executor[protocol.ResponseHolder]
}

func NewEthChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthChainValidator {
	return &EthChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: options.InternalTimeout,
		executor:        validations.ValidatorExecutor(upstreamId, "ethChainValidation", nil),
	}
}

func (c *EthChainValidator) Validate() validations.ValidationSettingResult {
	ctx, cancel := context.WithTimeout(context.Background(), c.internalTimeout)
	defer cancel()

	group, ctx := errgroup.WithContext(ctx)
	logger := zerolog.Ctx(ctx)

	var chainId, netVersion string

	group.Go(func() error {
		result, err := c.getChainId(ctx)
		if err != nil {
			logger.Error().Err(err).Msgf("failed to get chainId of upstream '%s'", c.upstreamId)
			return err
		}
		chainId = strings.ToLower(result)
		return nil
	})

	group.Go(func() error {
		result, err := c.getNetVersion(ctx)
		if err != nil {
			logger.Error().Err(err).Msgf("failed to get netVersion of upstream '%s'", c.upstreamId)
			return err
		}
		netVersion = strings.ToLower(result)
		return nil
	})

	if err := group.Wait(); err != nil {
		return validations.SettingsError
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

		return validations.FatalSettingError
	}

	return validations.Valid
}

func (c *EthChainValidator) getChainId(ctx context.Context) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, c.chain.Chain)
	if err != nil {
		return "", err
	}

	response, _ := c.executor.
		Get(func() (protocol.ResponseHolder, error) {
			return c.connector.SendRequest(ctx, request), nil
		})

	if response.HasError() {
		return "", response.GetError()
	}

	return response.ResponseResultString()
}

func (c *EthChainValidator) getNetVersion(ctx context.Context) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, c.chain.Chain)
	if err != nil {
		return "", err
	}

	response, _ := c.executor.
		Get(func() (protocol.ResponseHolder, error) {
			return c.connector.SendRequest(ctx, request), nil
		})

	if response.HasError() {
		return "", response.GetError()
	}

	result, err := response.ResponseResultString()
	if err != nil {
		return "", err
	}

	result = strings.ToLower(result)
	if strings.Contains(result, "x") {
		n, ok := new(big.Int).SetString(strings.TrimPrefix(result, "0x"), 16)
		if !ok {
			return "", fmt.Errorf("invalid hex netVersion %q", result)
		}
		return n.String(), nil
	}

	return result, nil
}

var _ validations.SettingsValidator = (*EthChainValidator)(nil)
