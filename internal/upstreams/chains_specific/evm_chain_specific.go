package specific

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/ethereum/go-ethereum/rpc"
)

type ChainSpecific interface {
	GetLatestBlock(ctx context.Context) (protocol.Block, error)
	GetFinalizedBlock(context.Context) (protocol.Block, error)

	ParseBlock([]byte) (protocol.Block, error)
	ParseSubscriptionBlock(data []byte) (protocol.Block, error)

	SubscribeHeadRequest() (protocol.RequestHolder, error)

	HealthValidators() []validations.Validator[protocol.AvailabilityStatus]
	SettingsValidators() []validations.Validator[validations.ValidationSettingResult]

	LowerBoundProcessor() lower_bounds.LowerBoundProcessor
	LabelsProcessor() labels.LabelsProcessor
}

type EvmChainSpecificObject struct {
	ctx        context.Context
	upstreamId string
	connector  connectors.ApiConnector
	chain      *chains.ConfiguredChain
	options    *chains.Options
}

func (e *EvmChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			e.upstreamId,
			e.connector,
			labels.NewEthClientLabelsDetector(e.upstreamId, e.chain.Chain, labels.EthMappingFunc),
			e.options.InternalTimeout,
		),
		labels.NewEthGasLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		labels.NewEthFlashBlockDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		labels.NewEthHLTxLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout*2, e.connector),
		labels.NewEthArchiveLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
	}

	return labels.NewBaseLabelsProcessor(e.ctx, e.upstreamId, labelsDetectors, e.options.ValidationInterval*5)
}

func (e *EvmChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return nil
}

func (e *EvmChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0)

	if *e.options.ValidateSyncing {
		validators = append(validators, validations.NewEthSyncingValidator(e.upstreamId, e.chain, e.connector, e.options.InternalTimeout))
	}
	if *e.options.ValidatePeers {
		validators = append(validators, validations.NewEthPeersValidator(e.upstreamId, e.chain.Chain, e.connector, e.options))
	}

	return validators
}

func (e *EvmChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	settingsValidators := make([]validations.Validator[validations.ValidationSettingResult], 0)

	if !*e.options.DisableChainValidation {
		settingsValidators = append(settingsValidators, validations.NewEthChainValidator(e.upstreamId, e.connector, e.chain, e.options))
	}
	if *e.options.ValidateCallLimit && e.chain.CallValidateContract != "" {
		settingsValidators = append(settingsValidators, validations.NewEthCallLimitValidator(e.upstreamId, e.connector, e.chain, e.options))
	}

	return settingsValidators
}

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.LatestBlockNumber)
}

func (e *EvmChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.FinalizedBlockNumber)
}

func (e *EvmChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (protocol.Block, error) {
	return e.ParseBlock(blockBytes)
}

func (e *EvmChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	evmBlock := EvmBlock{}
	err := sonic.Unmarshal(blockBytes, &evmBlock)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the evm block, reason - %s", err.Error())
	}
	if evmBlock.Height == nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the evm block, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		uint64(evmBlock.Height.Int64()),
		0,
		blockchain.NewHashIdFromString(evmBlock.Hash),
		blockchain.NewHashIdFromString(evmBlock.Parent),
	), nil
}

func (e *EvmChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []interface{}{"newHeads"}, e.chain.Chain)
}

func NewEvmChainSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EvmChainSpecificObject {
	return &EvmChainSpecificObject{
		ctx:        ctx,
		upstreamId: upstreamId,
		connector:  connector,
		chain:      chain,
		options:    options,
	}
}

func (e *EvmChainSpecificObject) getBlockByTag(ctx context.Context, connector connectors.ApiConnector, blockTag rpc.BlockNumber) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockByNumber", []interface{}{blockTag, false}, e.chain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	parsedBlock, err := e.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return parsedBlock, nil
}

type EvmBlock struct {
	Hash   string           `json:"hash"`
	Parent string           `json:"parentHash"`
	Height *rpc.BlockNumber `json:"number"`
}

var _ ChainSpecific = (*EvmChainSpecificObject)(nil)
