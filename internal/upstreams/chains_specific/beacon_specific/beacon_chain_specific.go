package beacon_specific

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/beacon_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/beacon_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/beacon_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// BeaconChainSpecificObject implements the REST-based, poll-only chain family for
// the Ethereum/Gnosis Beacon Chain (consensus layer). It has no websocket
// subscriptions - the head is polled via GetLatestBlock by the RpcHead the head
// processor builds for any RestConnector. Ported from dshackle's
// BeaconChainSpecific.
type BeaconChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	pollInterval    time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewBeaconChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *BeaconChainSpecificObject {
	return &BeaconChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		pollInterval:    pollInterval,
		configuredChain: configuredChain,
	}
}

// BlockProcessor reuses the eth-like processor purely for finalized-block
// detection: beacon exposes the finalized checkpoint header via
// GetFinalizedBlock (GET /eth/v1/beacon/headers/finalized). Safe-block detection
// is disabled - beacon has no "safe" head and doesn't implement GetSafeBlock.
func (b *BeaconChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewBaseBlockProcessor(
		b.ctx,
		b.upstreamId,
		b.pollInterval,
		b.internalTimeout,
		b.options.FinalizedBlockDetectionDisabled(),
		true,
		b.connector,
		b,
	)
}

// CapDetectors returns none: beacon chain is REST-only with no websocket connector,
// so the ws-derived WsCap can never be asserted.
func (b *BeaconChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (b *BeaconChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	clientDetector := eth_labels.NewEthClientLabelsDetector(
		b.upstreamId,
		b.configuredChain.Chain,
		beacon_labels.BeaconMappingFunc,
		func() (protocol.RequestHolder, error) {
			return protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/node/version", nil, b.configuredChain.Chain), nil
		},
	)
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(b.upstreamId, b.connector, clientDetector, b.internalTimeout),
	}
	return labels.NewBaseLabelsProcessor(b.ctx, b.upstreamId, labelsDetectors, b.labelsDelay)
}

func (b *BeaconChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := beacon_bounds.NewBeaconChainLowerBoundDetectors(
		b.upstreamId,
		b.configuredChain.Chain,
		b.internalTimeout,
		b.connector,
	)
	return lower_bounds.NewBaseLowerBoundProcessor(
		b.ctx,
		b.upstreamId,
		b.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (b *BeaconChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if b.options != nil && *b.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}

	chain := b.configuredChain.Chain
	validators := []validations.Validator[protocol.AvailabilityStatus]{
		beacon_validations.NewBeaconChainHealthValidator(b.upstreamId, b.connector, chain, b.internalTimeout),
	}
	if boolValue(b.options.ValidateSyncing) {
		validators = append(validators, beacon_validations.NewBeaconChainSyncingValidator(b.upstreamId, b.connector, chain, b.internalTimeout))
	}
	if boolValue(b.options.ValidatePeers) && b.options.MinPeers > 0 {
		validators = append(validators, beacon_validations.NewBeaconChainPeersValidator(b.upstreamId, b.connector, chain, b.options.MinPeers, b.internalTimeout))
	}
	return validators
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func (b *BeaconChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (b *BeaconChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	return b.fetchHeader(ctx, "head")
}

func (b *BeaconChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return b.fetchHeader(ctx, "finalized")
}

func (b *BeaconChainSpecificObject) fetchHeader(ctx context.Context, blockId string) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest(
		"GET#/eth/v1/beacon/headers/*",
		&protocol.RequestParams{PathParams: []string{blockId}},
		b.configuredChain.Chain,
	)
	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}
	return b.ParseBlock(response.ResponseResult())
}

// ParseBlock decodes a GET /eth/v1/beacon/headers/{id} response. The slot is used
// as the block height (beacon has no separate block number); the Slot field stays
// 0 as it is Solana-specific. root/parent_root map to hash/parentHash.
func (b *BeaconChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	var header beaconHeaderResponse
	if err := sonic.Unmarshal(blockBytes, &header); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the beacon header, reason - %s", err.Error())
	}

	slotStr := header.Data.Header.Message.Slot
	if slotStr == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the beacon header, got '%s'", string(blockBytes))
	}
	slot, err := strconv.ParseUint(slotStr, 10, 64)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the beacon slot '%s', reason - %s", slotStr, err.Error())
	}

	return protocol.NewBlock(
		slot,
		0,
		hashOrEmpty(header.Data.Root),
		hashOrEmpty(header.Data.Header.Message.ParentRoot),
	), nil
}

func (b *BeaconChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("beacon chain does not support websocket subscriptions")
}

func (b *BeaconChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("beacon chain does not support websocket subscriptions")
}

func hashOrEmpty(value string) blockchain.HashId {
	if value == "" {
		return blockchain.EmptyHash
	}
	return blockchain.NewHashIdFromString(value)
}

type beaconHeaderResponse struct {
	Data struct {
		Root   string `json:"root"`
		Header struct {
			Message struct {
				Slot       string `json:"slot"`
				ParentRoot string `json:"parent_root"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

var _ chains_specific.ChainSpecific = (*BeaconChainSpecificObject)(nil)
