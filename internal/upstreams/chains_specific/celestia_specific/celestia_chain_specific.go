package celestia_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/celestia_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/celestia_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type CelestiaChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	pollInterval    time.Duration
	internalTimeout time.Duration
	configuredChain *chains.ConfiguredChain
	options         *chains.Options
}

func NewCelestiaChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *CelestiaChainSpecificObject {
	return &CelestiaChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		pollInterval:    pollInterval,
		options:         options,
		internalTimeout: options.InternalTimeout,
		configuredChain: configuredChain,
	}
}

// CometBFT finality is instant, so the finalized pointer follows the head and
// there is no separate "safe" block - safe block detection is disabled.
func (c *CelestiaChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
		c.ctx,
		c.upstreamId,
		c.pollInterval,
		c.internalTimeout,
		c.options.FinalizedBlockDetectionDisabled(),
		true,
		c.connector,
		c,
	)
}

// CapDetectors returns nil: celestia is served over json-rpc only (the go-jsonrpc
// channel protocol is not supported by the ws connector), so no ws-derived cap
// can be asserted.
func (c *CelestiaChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

// MethodsProcessor returns nil: celestia-node has no way to ask which methods it
// implements, so upstreams keep the full method set the spec declares.
func (c *CelestiaChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

// node.Info requires an admin token, so client labels can't be detected.
func (c *CelestiaChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	return nil
}

func (c *CelestiaChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		celestia_bounds.NewCelestiaLowerBoundDetector(
			c.upstreamId,
			c.configuredChain.Chain,
			c.internalTimeout,
			c.connector,
		),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(
		c.ctx,
		c.upstreamId,
		c.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (c *CelestiaChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if c.options != nil && *c.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		celestia_validations.NewCelestiaHealthValidator(
			c.upstreamId, c.connector, c.configuredChain.Chain, c.internalTimeout,
		),
	}
}

func (c *CelestiaChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if c.configuredChain == nil || c.configuredChain.ChainId == "" {
		return nil
	}
	if c.options != nil && *c.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		celestia_validations.NewCelestiaChainValidator(c.upstreamId, c.connector, c.configuredChain, c.internalTimeout),
	}
}

func (c *CelestiaChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"header.LocalHead",
		[]interface{}{},
		c.configuredChain.Chain,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	response := c.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	return c.ParseBlock(response.ResponseResult())
}

// CometBFT commits are final: a header the node has stored can't be reorged out, so
// the finalized pointer is the head itself.
func (c *CelestiaChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return c.GetLatestBlock(ctx)
}

func (c *CelestiaChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	header, err := specific_helpers.ParseCelestiaExtendedHeader(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	height, err := header.Height()
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf(
			"couldn't parse the celestia header height, reason - %s", err.Error(),
		)
	}
	return protocol.NewBlock(
		height,
		0,
		blockchain.NewHashIdFromString(header.Commit.BlockId.Hash),
		blockchain.NewHashIdFromString(header.Header.LastBlockId.Hash),
	), nil
}

// celestia-node subscriptions use the go-jsonrpc channel protocol (xrpc.ch.val),
// which the ws connector doesn't speak yet.
func (c *CelestiaChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("celestia does not support websocket subscriptions")
}

func (c *CelestiaChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("celestia does not support websocket subscriptions")
}

var _ chains_specific.ChainSpecific = (*CelestiaChainSpecificObject)(nil)
