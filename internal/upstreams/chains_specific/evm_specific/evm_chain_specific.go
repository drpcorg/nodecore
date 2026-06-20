package evm_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/ethereum/go-ethereum/rpc"
)

type EvmChainSpecificObject struct {
	ctx          context.Context
	upstreamId   string
	pollInterval time.Duration
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
}

func (e *EvmChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewEthLikeBlockProcessor(
		e.ctx,
		e.upstreamId,
		e.pollInterval,
		e.options.InternalTimeout,
		e.options.FinalizedBlockDetectionDisabled(),
		e.options.SafeBlockDetectionDisabled(),
		e.connector,
		e,
	)
}

func (e *EvmChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			e.upstreamId,
			e.connector,
			eth_labels.NewEthClientLabelsDetector(e.upstreamId, e.chain.Chain, eth_labels.EthMappingFunc),
			e.options.InternalTimeout,
		),
		eth_labels.NewEthGasLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		eth_labels.NewEthFlashBlockDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		eth_labels.NewEthHLTxLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout*2, e.connector),
		archiveLabelsDetector(e),
	}

	return labels.NewBaseLabelsProcessor(e.ctx, e.upstreamId, labelsDetectors, e.options.ValidationInterval*5)
}

func archiveLabelsDetector(e *EvmChainSpecificObject) labels.LabelsDetector {
	if e.options.ArchiveCapability != nil && !*e.options.ArchiveCapability {
		return labels.NewStaticLabelsDetector(map[string]string{"archive": "false"})
	}
	return eth_labels.NewEthArchiveLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector)
}

func (e *EvmChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		evm_bounds.NewEvmStateLowerBoundDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		evm_bounds.NewEvmBlockLowerBoundDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		evm_bounds.NewEvmTxLowerBoundDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		evm_bounds.NewEvmReceiptsLowerBoundDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
	}
	if e.hasMethod("eth_getProof") {
		detectors = append(detectors, evm_bounds.NewEvmProofLowerBoundDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector))
	}
	return lower_bounds.NewBaseLowerBoundProcessor(e.ctx, e.upstreamId, e.chain.AverageRemoveSpeed(), detectors)
}

func (e *EvmChainSpecificObject) hasMethod(methodName string) bool {
	if e.chain == nil {
		return false
	}
	specName := e.chain.MethodSpec
	if specName == "" {
		specName = chains.GetMethodSpecNameByChain(e.chain.Chain)
	}
	return specName != "" && specs.GetSpecMethod(specName, methodName) != nil
}

func (e *EvmChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0)

	if *e.options.ValidateSyncing {
		validators = append(validators, eth_validations.NewEthSyncingValidator(e.upstreamId, e.chain, e.connector, e.options.InternalTimeout))
	}
	if *e.options.ValidatePeers {
		validators = append(validators, eth_validations.NewEthPeersValidator(e.upstreamId, e.chain.Chain, e.connector, e.options))
	}

	return validators
}

func (e *EvmChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	settingsValidators := make([]validations.Validator[validations.ValidationSettingResult], 0)

	if !*e.options.DisableChainValidation {
		settingsValidators = append(settingsValidators, eth_validations.NewEthChainValidator(e.upstreamId, e.connector, e.chain, e.options))
	}
	if *e.options.ValidateCallLimit && e.chain.CallValidateContract != "" {
		settingsValidators = append(settingsValidators, eth_validations.NewEthCallLimitValidator(e.upstreamId, e.connector, e.chain, e.options))
	}
	if e.options.ValidateClientVersion != nil && *e.options.ValidateClientVersion {
		settingsValidators = append(settingsValidators, eth_validations.NewEthClientVersionValidator(e.upstreamId, e.connector, e.chain, e.options))
	}
	if len(e.chain.GasPriceCondition) > 0 {
		settingsValidators = append(settingsValidators, eth_validations.NewEthGasPriceValidator(e.upstreamId, e.connector, e.chain, e.options))
	}
	if e.options.DisableLogIndexValidation == nil || !*e.options.DisableLogIndexValidation {
		settingsValidators = append(settingsValidators, eth_validations.NewEthLogIndexValidator(e.upstreamId, e.connector, e.chain, e.options))
	}

	return settingsValidators
}

func (e *EvmChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.LatestBlockNumber)
}

func (e *EvmChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.FinalizedBlockNumber)
}

func (e *EvmChainSpecificObject) GetSafeBlock(ctx context.Context) (protocol.Block, error) {
	return e.getBlockByTag(ctx, e.connector, rpc.SafeBlockNumber)
}

func (e *EvmChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (protocol.Block, error) {
	block, err := e.ParseBlock(blockBytes)
	if err != nil {
		return block, err
	}
	block.RawData = append([]byte(nil), blockBytes...)
	return block, nil
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
	pollInterval time.Duration,
	options *chains.Options,
) *EvmChainSpecificObject {
	return &EvmChainSpecificObject{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
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

var _ chains_specific.ChainSpecific = (*EvmChainSpecificObject)(nil)
