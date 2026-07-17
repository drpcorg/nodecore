package bitcoin_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: bitcoind has no push-style head notification we can
// consume (no ZMQ/WS in scope), so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("bitcoin: head subscriptions are not supported")

type BitcoinChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewBitcoinChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *BitcoinChainSpecificObject {
	return &BitcoinChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (b *BitcoinChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

// LabelsProcessor has no detectors yet - client-version labelling lands in a
// later task. NewBaseLabelsProcessor returns nil for an empty detector list,
// which satisfies the labels.LabelsProcessor interface as a no-op.
func (b *BitcoinChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	return labels.NewBaseLabelsProcessor(b.ctx, b.upstreamId, []labels.LabelsDetector{}, b.labelsDelay)
}

func (b *BitcoinChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(b.upstreamId, input.WsConnector)
}

// LowerBoundProcessor has no detectors yet - prune-aware bounds land in a
// later task. NewBaseLowerBoundProcessor returns nil for an empty detector
// list, which satisfies the lower_bounds.LowerBoundProcessor interface as a
// no-op.
func (b *BitcoinChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return lower_bounds.NewBaseLowerBoundProcessor(
		b.ctx,
		b.upstreamId,
		b.configuredChain.AverageRemoveSpeed(),
		[]lower_bounds.LowerBoundDetector{},
	)
}

// HealthValidators has no validators yet - health/syncing checks land in a
// later task; the disable-option short-circuit is kept so the shape matches
// what every other family already does.
func (b *BitcoinChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if b.options != nil && *b.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{}
}

// SettingsValidators has no validators yet - genesis-hash chain validation
// lands in a later task; the disable-option short-circuit is kept so the
// shape matches what every other family already does.
func (b *BitcoinChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if b.configuredChain == nil || b.configuredChain.ChainId == "" {
		return nil
	}
	if b.options != nil && *b.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{}
}

// GetLatestBlock polls getbestblockhash, then getblockheader on that hash to
// pick up height/hash/parent-hash. bitcoind has no head subscription, so this
// poll is the only way head tracking learns about a new block.
func (b *BitcoinChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	hashRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("getbestblockhash", []any{}, b.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	hashResponse := b.connector.SendRequest(ctx, hashRequest)
	if hashResponse.HasError() {
		return protocol.ZeroBlock{}, hashResponse.GetError()
	}
	hash := protocol.ResultAsString(hashResponse.ResponseResult())
	if hash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("bitcoin upstream '%s' has no best block hash", b.upstreamId)
	}

	headerRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("getblockheader", []any{hash}, b.configuredChain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	headerResponse := b.connector.SendRequest(ctx, headerRequest)
	if headerResponse.HasError() {
		return protocol.ZeroBlock{}, headerResponse.GetError()
	}

	block, err := b.ParseBlock(headerResponse.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse bitcoin block header for hash '%s': %w", hash, err)
	}
	return block, nil
}

// GetFinalizedBlock delegates to GetLatestBlock: bitcoind has no separate
// finalized-head notion in scope here (confirmations-based finality is a
// follow-up), same contract as algorand's pure-poll head tracking.
func (b *BitcoinChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return b.GetLatestBlock(ctx)
}

// ParseBlock expects the payload shape of getblockheader (verbose, the
// default): {"hash", "height", "previousblockhash", ...}. Both Bitcoin Core
// and Dogecoin Core share this shape.
func (b *BitcoinChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	header := bitcoinBlockHeader{}
	if err := sonic.Unmarshal(blockBytes, &header); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the bitcoin block header, reason - %s", err.Error())
	}
	if header.Hash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the bitcoin block header, got '%s'", string(blockBytes))
	}

	parentHash := blockchain.EmptyHash
	if header.PreviousBlockHash != "" {
		parentHash = blockchain.NewHashIdFromString(header.PreviousBlockHash)
	}

	return protocol.NewBlock(
		header.Height,
		0,
		blockchain.NewHashIdFromString(header.Hash),
		parentHash,
	), nil
}

func (b *BitcoinChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (b *BitcoinChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type bitcoinBlockHeader struct {
	Hash              string `json:"hash"`
	Height            uint64 `json:"height"`
	PreviousBlockHash string `json:"previousblockhash"`
}

var _ chains_specific.ChainSpecific = (*BitcoinChainSpecificObject)(nil)
