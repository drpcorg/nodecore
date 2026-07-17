package near_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/near_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/near_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/near_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: nearcore's JSON-RPC is HTTP request/response only,
// there is no subscription transport at all, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("near: head subscriptions are not supported")

type NearChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewNearChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *NearChainSpecificObject {
	return &NearChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (n *NearChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (n *NearChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			n.upstreamId,
			n.connector,
			near_labels.NewNearClientLabelsDetector(n.configuredChain.Chain),
			n.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(n.ctx, n.upstreamId, labelsDetectors, n.labelsDelay)
}

func (n *NearChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(n.upstreamId, input.WsConnector)
}

func (n *NearChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		near_bounds.NewNearLowerBoundDetector(
			n.upstreamId,
			n.configuredChain.Chain,
			n.internalTimeout,
			n.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		n.ctx,
		n.upstreamId,
		n.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (n *NearChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if n.options != nil && *n.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	validatePeers := n.options != nil && n.options.ValidatePeers != nil && *n.options.ValidatePeers
	return []validations.Validator[protocol.AvailabilityStatus]{
		near_validations.NewNearHealthValidator(
			n.upstreamId, n.connector, n.configuredChain, n.internalTimeout, validatePeers,
		),
	}
}

func (n *NearChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if n.configuredChain == nil || n.configuredChain.ChainId == "" {
		return nil
	}
	if n.options != nil && *n.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		near_validations.NewNearChainValidator(n.upstreamId, n.connector, n.configuredChain, n.internalTimeout),
	}
}

// GetLatestBlock polls the optimistic head - the same head dshackle exposed
// for near, ~3 blocks ahead of "final".
func (n *NearChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return n.fetchBlockByFinality(ctx, "optimistic")
}

// GetFinalizedBlock fetches the "final" head. It is not tracked in a separate
// poll loop (BlockProcessor is nil), but callers asking explicitly get the
// correct finalized block instead of an optimistic alias.
func (n *NearChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return n.fetchBlockByFinality(ctx, "final")
}

func (n *NearChainSpecificObject) fetchBlockByFinality(ctx context.Context, finality string) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"block",
		map[string]any{"finality": finality},
		n.configuredChain.Chain,
	)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	response := n.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := n.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the near %s block: %w", finality, err)
	}
	return block, nil
}

// ParseBlock expects the payload shape of the `block` method:
// {"author": ..., "header": {"height", "hash", "prev_hash", ...}, "chunks": [...]}.
// Hashes are base58 strings and are used verbatim as block identifiers.
func (n *NearChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	block := nearBlock{}
	if err := sonic.Unmarshal(blockBytes, &block); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the near block, reason - %s", err.Error())
	}
	if block.Header.Hash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the near block, got '%s'", string(blockBytes))
	}

	parentHash := blockchain.EmptyHash
	if block.Header.PrevHash != "" {
		parentHash = blockchain.NewHashIdFromString(block.Header.PrevHash)
	}

	return protocol.NewBlock(
		block.Header.Height,
		0,
		blockchain.NewHashIdFromString(block.Header.Hash),
		parentHash,
	), nil
}

func (n *NearChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (n *NearChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type nearBlock struct {
	Header nearBlockHeader `json:"header"`
}

type nearBlockHeader struct {
	Height   uint64 `json:"height"`
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
}

var _ chains_specific.ChainSpecific = (*NearChainSpecificObject)(nil)
