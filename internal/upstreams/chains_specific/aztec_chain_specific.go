package specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AztecChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func (a *AztecChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(a.upstreamId, a.connector, labels.NewAztecClientLabelsDetector(), a.internalTimeout),
	}
	return labels.NewBaseLabelsProcessor(a.ctx, a.upstreamId, labelsDetectors, a.labelsDelay)
}

func (a *AztecChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		lower_bounds.NewAztecLowerBoundDetector(a.upstreamId, a.internalTimeout, a.connector),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(a.ctx, a.upstreamId, a.configuredChain.AverageRemoveSpeed(), detectors)
}

func (a *AztecChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return []validations.Validator[protocol.AvailabilityStatus]{
		validations.NewAztecHealthValidator(a.upstreamId, a.connector, a.internalTimeout),
	}
}

func (a *AztecChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if a.configuredChain == nil || a.configuredChain.ChainId == "" {
		return nil
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		validations.NewAztecChainValidator(a.upstreamId, a.connector, a.configuredChain, a.internalTimeout),
	}
}

func (a *AztecChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getL2Tips", []interface{}{}, chains.AZTEC_MAINNET)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AztecChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getL2Tips", []interface{}{}, chains.AZTEC_MAINNET)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	tips := AztecL2Tips{}
	if err := sonic.Unmarshal(response.ResponseResult(), &tips); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse aztec L2 tips, reason - %s", err.Error())
	}
	if tips.Proven.Number == 0 {
		return protocol.ZeroBlock{}, nil
	}
	return protocol.NewBlock(
		tips.Proven.Number,
		0,
		blockchain.NewHashIdFromString(tips.Proven.Hash),
		blockchain.EmptyHash,
	), nil
}

func (a *AztecChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	tips := AztecL2Tips{}
	err := sonic.Unmarshal(blockBytes, &tips)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the aztec L2 tips, reason - %s", err.Error())
	}

	height := tips.Proposed.Number
	if height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the aztec L2 tips, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(
		height,
		0,
		blockchain.NewHashIdFromString(tips.Proposed.Hash),
		blockchain.EmptyHash,
	), nil
}

func (a *AztecChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("aztec does not support websocket subscriptions")
}

func (a *AztecChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("aztec does not support websocket subscriptions")
}

func NewAztecChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	internalTimeout, labelsDelay time.Duration,
) *AztecChainSpecificObject {
	return &AztecChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
		labelsDelay:     labelsDelay,
		configuredChain: configuredChain,
	}
}

// AztecL2Tips models the response from node_getL2Tips. Aztec reshaped the payload
// between v3 and v4: in v3 every tip was flat ({number, hash}); in v4 proven,
// finalized and checkpointed each became {block: {number, hash}, checkpoint: {...}}
// while proposed stayed flat. AztecVersionedTip handles both shapes transparently
// so a node on either version is parsed correctly.
type AztecL2Tips struct {
	Proposed     AztecTip          `json:"proposed"`
	Proven       AztecVersionedTip `json:"proven"`
	Finalized    AztecVersionedTip `json:"finalized"`
	Checkpointed AztecVersionedTip `json:"checkpointed"`
}

type AztecTip struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

// AztecVersionedTip flattens v3 ({number, hash}) and v4 ({block: {number, hash}})
// shapes into a single {Number, Hash} pair.
type AztecVersionedTip struct {
	Number uint64
	Hash   string
}

func (a *AztecVersionedTip) UnmarshalJSON(data []byte) error {
	var nested struct {
		Block *AztecTip `json:"block"`
	}
	if err := sonic.Unmarshal(data, &nested); err == nil && nested.Block != nil {
		a.Number = nested.Block.Number
		a.Hash = nested.Block.Hash
		return nil
	}
	var flat AztecTip
	if err := sonic.Unmarshal(data, &flat); err != nil {
		return err
	}
	a.Number = flat.Number
	a.Hash = flat.Hash
	return nil
}

var _ ChainSpecific = (*AztecChainSpecificObject)(nil)
