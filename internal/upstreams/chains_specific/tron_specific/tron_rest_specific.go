package tron_specific

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tron_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tron_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

type TronRestSpecific struct {
	ctx          context.Context
	upstreamId   string
	pollInterval time.Duration
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
}

func (t *TronRestSpecific) BlockProcessor() blocks.BlockProcessor {
	disableSafeBlockDetection := t.options.DisableSafeBlockDetection != nil && *t.options.DisableSafeBlockDetection
	return blocks.NewEthLikeBlockProcessor(
		t.ctx,
		t.upstreamId,
		t.pollInterval,
		t.options.InternalTimeout,
		disableSafeBlockDetection,
		t.connector,
		t,
	)
}

func (t *TronRestSpecific) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	body := []byte(`{"detail": false}`)
	request := protocol.NewInternalUpstreamRestRequestWithBody("POST", "/wallet/getblock", body, t.chain.Chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}
	parsedBlock, err := t.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return parsedBlock, nil
}

func (t *TronRestSpecific) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest("POST", "/wallet/getnodeinfo", t.chain.Chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	type nodeInfo struct {
		SolidityBlock string `json:"solidityBlock"`
	}
	nodeInfoValue := nodeInfo{}
	err := sonic.Unmarshal(response.ResponseResult(), &nodeInfoValue)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	parts := strings.Split(nodeInfoValue.SolidityBlock, ",")
	if len(parts) < 1 {
		return protocol.ZeroBlock{}, fmt.Errorf("invalid solidity block")
	}
	numParts := strings.Split(parts[0], ":")
	if len(numParts) != 2 {
		return protocol.ZeroBlock{}, fmt.Errorf("invalid solidity block")
	}
	num, err := strconv.ParseUint(numParts[1], 10, 64)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return protocol.NewBlockWithHeight(num), nil
}

func (t *TronRestSpecific) ParseBlock(bytes []byte) (protocol.Block, error) {
	block := TronRestBlock{}
	err := sonic.Unmarshal(bytes, &block)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the tron block, reason - %s", err.Error())
	}
	return protocol.NewBlock(
		block.Header.Data.Number,
		0,
		blockchain.NewHashIdFromString(block.Hash),
		blockchain.NewHashIdFromString(block.Header.Data.ParentHash),
	), nil
}

func (t *TronRestSpecific) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (t *TronRestSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, nil
}

func (t *TronRestSpecific) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0)

	if *t.options.ValidatePeers {
		validators = append(validators, tron_validations.NewTronPeersValidator(t.upstreamId, t.chain.Chain, t.connector, t.options))
	}
	if *t.options.ValidateSyncing {
		validators = append(validators, tron_validations.NewTronSyncingValidator(t.upstreamId, t.chain, t.connector, t.options.InternalTimeout))
	}

	return validators
}

func (t *TronRestSpecific) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (t *TronRestSpecific) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		tron_bounds.NewTronLowerBoundDetector(t.upstreamId, t.chain.Chain, t.options.InternalTimeout, t.connector),
	}

	return lower_bounds.NewBaseLowerBoundProcessor(t.ctx, t.upstreamId, t.chain.AverageRemoveSpeed(), detectors)
}

func (t *TronRestSpecific) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			tron_labels.NewTronClientLabelsDetector(t.chain.Chain),
			t.options.InternalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.options.ValidationInterval*5)
}

type TronRestBlock struct {
	Hash   string `json:"blockID"`
	Header Header `json:"block_header"`
}

type Header struct {
	Data RawData `json:"raw_data"`
}

type RawData struct {
	Number     uint64 `json:"number"`
	ParentHash string `json:"parentHash"`
}

func newTronRestSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (*TronRestSpecific, error) {
	if connector == nil {
		return nil, fmt.Errorf("no connector specified")
	}
	if connector.GetType() != specs.RestConnector {
		return nil, fmt.Errorf("tron rest specific supports only rest connector")
	}
	return &TronRestSpecific{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
	}, nil
}

func NewTronSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (chains_specific.ChainSpecific, error) {
	if connector == nil {
		return nil, fmt.Errorf("no connector specified")
	}
	switch connector.GetType() {
	case specs.RestConnector:
		return newTronRestSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	case specs.JsonRpcConnector:
		return evm_specific.NewEvmChainSpecific(ctx, upstreamId, connector, chain, pollInterval, options), nil
	default:
		return nil, fmt.Errorf("tron specific supports only json-rpc or rest connector but not %s", connector.GetType())
	}
}

var _ chains_specific.ChainSpecific = (*TronRestSpecific)(nil)
