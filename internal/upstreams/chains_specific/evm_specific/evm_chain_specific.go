package evm_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/caps/evm_caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/methods/evm_methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/samber/lo"
)

type EvmChainSpecificObject struct {
	ctx           context.Context
	upstreamId    string
	pollInterval  time.Duration
	connector     connectors.ApiConnector
	allConnectors []connectors.ApiConnector
	chain         *chains.ConfiguredChain
	options       *chains.Options
	manualLabels  map[string]string
}

func (e *EvmChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
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
	return labels.NewGenericLabelsProcessor(e.ctx, e.upstreamId, e.labelsDetectors(), e.options.ValidationInterval*5)
}

func (e *EvmChainSpecificObject) labelsDetectors() []labels.LabelsDetector {
	restAdditional, _ := lo.Find(e.allConnectors, func(c connectors.ApiConnector) bool {
		return c.GetType() == specs.RestAdditional
	})

	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			e.upstreamId,
			e.connector,
			eth_labels.NewEthClientLabelsDetector(e.upstreamId, e.chain.Chain, eth_labels.EthMappingFunc, func() (protocol.RequestHolder, error) {
				return protocol.NewInternalUpstreamJsonRpcRequest("web3_clientVersion", nil, e.chain.Chain)
			}),
			e.options.InternalTimeout,
		),
		eth_labels.NewEthGasLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		eth_labels.NewEthFlashBlockDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		eth_labels.NewEthHLTxLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout*2, e.connector),
		eth_labels.NewEthAllInfoLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, restAdditional),
	}
	if !archiveDetectionSuppressed(e.manualLabels) {
		labelsDetectors = append(
			labelsDetectors,
			eth_labels.NewEthArchiveLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		)
	}

	return labelsDetectors
}

// MethodsProcessor detects which of the chain spec's methods this node actually serves.
//
// The two detectors are peers rather than stages: rpc_modules attributes methods to modules
// wholesale, while the probes settle the handful of methods a present module does not
// guarantee. Their verdicts are unioned, so an inconclusive probe can never resurrect a
// method whose module the node does not report - ordering them buys nothing.
func (e *EvmChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
	base := e.detectableMethods()
	detectors := []methods.MethodsDetector{
		evm_methods.NewRpcModulesDetector(e.upstreamId, e.chain.Chain, e.connector, e.options.InternalTimeout, base),
		evm_methods.NewMethodProbeDetector(e.upstreamId, e.chain.Chain, e.connector, e.options.InternalTimeout, base),
	}

	return methods.NewGenericMethodsProcessor(e.ctx, e.upstreamId, detectors, methods.DetectionInterval)
}

// detectableMethods is the set of spec methods the detectors above may form an opinion
// about - the chain spec's methods restricted to the connectors that speak JSON-RPC.
func (e *EvmChainSpecificObject) detectableMethods() mapset.Set[string] {
	return methods.DetectableMethods(e.chain.MethodSpec, detectableConnectorTypes(e.allConnectors))
}

// detectableConnectorTypes narrows the upstream's connectors to the ones both detectors
// reason about. Their whole evidence base is JSON-RPC: rpc_modules is asked over the
// internal JSON-RPC connector and attributes a method by its module prefix, and the probes
// are JSON-RPC calls. A method served by any other connector - a REST path from a
// rest-additional spec, say - is invisible to that evidence, yet moduleOf would happily
// read the segment before the first underscore of "GET#/api/v1/node_info" as a module,
// find no node reporting it, and strip the method. Feeding those methods in at all is the
// bug; leaving them out is the fix.
func detectableConnectorTypes(apiConnectors []connectors.ApiConnector) []specs.ApiConnectorType {
	types := lo.Map(apiConnectors, func(item connectors.ApiConnector, index int) specs.ApiConnectorType {
		return item.GetType()
	})

	return lo.Filter(types, func(connectorType specs.ApiConnectorType, index int) bool {
		// Websocket counts: its methods (eth_subscribe and friends) are JSON-RPC in shape and
		// carry a real module prefix, so module attribution holds for them too.
		return connectorType == specs.JsonRpcConnector || connectorType == specs.WebsocketConnector
	})
}

// archiveDetectionSuppressed reports whether the upstream's manual 'archive' label
// pins the value to false. In that case the runtime archive probe must not run, so
// the configured value - seeded into the upstream state at construction - stands for
// the process lifetime. Any other value (including "true") lets the detector run and
// publish what it finds.
func archiveDetectionSuppressed(manualLabels map[string]string) bool {
	return manualLabels[chains.ArchiveLabel] == "false"
}

func (e *EvmChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	// one eth_capabilities cache per upstream, shared by all its detectors
	capabilities := evm_bounds.NewEvmCapabilities(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector)
	detectors := []lower_bounds.LowerBoundDetector{
		evm_bounds.NewEvmStateLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmBlockLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmTxLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities),
		evm_bounds.NewEvmReceiptsLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities),
	}
	if e.hasMethod("eth_getProof") {
		proofDetector := evm_bounds.NewEvmProofLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities)
		if e.hasMethod("debug_proofsSyncStatus") {
			proofDetector = proofDetector.WithProofsSyncStatus(evm_bounds.NewEvmProofsSyncStatus(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector))
		}
		detectors = append(detectors, proofDetector)
	}
	return lower_bounds.NewGenericLowerBoundProcessor(e.ctx, e.upstreamId, e.chain.AverageRemoveSpeed(), detectors)
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

func (e *EvmChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	wsCapName := fmt.Sprintf("%s_ws_cap", e.upstreamId)
	var wsCapDetector caps.CapDetector
	gateOnLiveness := input.HeadConnector != nil &&
		input.HeadConnector.GetType() == specs.WebsocketConnector &&
		input.Head != nil &&
		!e.options.LivenessSubscriptionValidationDisabled()
	if gateOnLiveness {
		// The head is ws-driven and liveness validation is enabled, so gate WsCap on
		// head liveness: a flapping head pulls the upstream out of subscription serving
		// (it still serves regular RPC).
		wsCapDetector = caps.NewWsHeadLivenessCapDetector(e.upstreamId, wsCapName, protocol.WsCap, input.WsConnector, input.Head, e.chain.Settings.ExpectedBlockTime)
	} else {
		// Poll-driven head, or liveness validation disabled: WsCap stays ungated (plain
		// ws presence).
		wsCapDetector = caps.NewWsPresenceCapDetector(wsCapName, protocol.WsCap, input.WsConnector)
	}

	detectors := []caps.CapDetector{
		wsCapDetector,
		evm_caps.NewEvmHeadSubCapDetector(fmt.Sprintf("%s_head_sub_cap", e.upstreamId), input.HeadConnector, input.Methods),
	}

	pendingTxName := fmt.Sprintf("%s_pending_tx_cap", e.upstreamId)
	if e.chain.Chain == chains.BASE {
		detectors = append(detectors, evm_caps.NewEvmPendingTxCapDetector(
			pendingTxName,
			input.WsConnector,
			e.connector,
			e.chain.Chain,
			e.options.InternalTimeout,
			evm_caps.BaseTxLimit,
		))
	} else {
		detectors = append(detectors, caps.NewWsPresenceCapDetector(pendingTxName, protocol.PendingTxCap, input.WsConnector))
	}

	return detectors
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
	allConnectors []connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
	manualLabels map[string]string,
) *EvmChainSpecificObject {
	return &EvmChainSpecificObject{
		ctx:           ctx,
		upstreamId:    upstreamId,
		connector:     connector,
		allConnectors: allConnectors,
		chain:         chain,
		options:       options,
		pollInterval:  pollInterval,
		manualLabels:  manualLabels,
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
