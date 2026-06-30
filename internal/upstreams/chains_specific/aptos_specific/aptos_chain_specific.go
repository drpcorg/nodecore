package aptos_specific

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/aptos_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/aptos_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aptos_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type AptosChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewAptosChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *AptosChainSpecificObject {
	return &AptosChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (a *AptosChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	info, err := a.fetchLedgerInfo(ctx)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	height, err := aptos_validations.ParseU64(info.BlockHeight)
	if err != nil || height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("aptos upstream '%s' returned invalid block_height '%s'", a.upstreamId, info.BlockHeight)
	}
	version, _ := aptos_validations.ParseU64(info.LedgerVersion)

	hash, err := a.fetchBlockHash(ctx, height)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't fetch aptos block %d: %w", height, err)
	}
	parent := blockchain.NewHashIdFromBytes(heightToBytes(subOne(height)))
	return protocol.NewBlock(height, version, hash, parent), nil
}

func (a *AptosChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	// Aptos has deterministic BFT finality: the latest committed block is final.
	return a.GetLatestBlock(ctx)
}

func (a *AptosChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	var info aptos_validations.AptosLedgerInfo
	if err := sonic.Unmarshal(blockBytes, &info); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the aptos ledger info, reason - %s", err.Error())
	}
	height, err := aptos_validations.ParseU64(info.BlockHeight)
	if err != nil || height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the aptos ledger info, got '%s'", string(blockBytes))
	}
	return protocol.NewBlock(height, 0, blockchain.EmptyHash, blockchain.EmptyHash), nil
}

func (a *AptosChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("aptos does not support websocket subscriptions")
}

func (a *AptosChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("aptos does not support websocket subscriptions")
}

func (a *AptosChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if a.options != nil && *a.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		aptos_validations.NewAptosHealthValidator(a.upstreamId, a.connector, a.configuredChain.Chain, a.internalTimeout),
	}
}

func (a *AptosChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if a.configuredChain == nil || a.configuredChain.ChainId == "" {
		return nil
	}
	if a.options != nil && *a.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		aptos_validations.NewAptosChainValidator(a.upstreamId, a.connector, a.configuredChain, a.internalTimeout),
	}
}

func (a *AptosChainSpecificObject) CapDetectors(input caps.DetectorInput) []caps.CapDetector {
	return caps.DefaultCapDetectors(a.upstreamId, input.WsConnector)
}

func (a *AptosChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		aptos_bounds.NewAptosLowerBoundDetector(a.upstreamId, a.configuredChain.Chain, a.internalTimeout, a.connector),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(a.ctx, a.upstreamId, a.configuredChain.AverageRemoveSpeed(), detectors)
}

func (a *AptosChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			a.upstreamId,
			a.connector,
			aptos_labels.NewAptosClientLabelsDetector(a.configuredChain.Chain),
			a.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(a.ctx, a.upstreamId, labelsDetectors, a.labelsDelay)
}

func (a *AptosChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (a *AptosChainSpecificObject) fetchLedgerInfo(ctx context.Context) (*aptos_validations.AptosLedgerInfo, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/v1", nil, a.configuredChain.Chain)
	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var info aptos_validations.AptosLedgerInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return nil, fmt.Errorf("couldn't parse aptos ledger info: %w", err)
	}
	return &info, nil
}

// fetchBlockHash reads the block hash for the given height. Aptos returns it as
// a 0x-prefixed 32-byte hex string; on a decode miss we fall back to a
// deterministic encoding of the height so HeadEvents never carry an empty hash.
func (a *AptosChainSpecificObject) fetchBlockHash(ctx context.Context, height uint64) (blockchain.HashId, error) {
	request := protocol.NewInternalUpstreamRestRequest(
		"GET#/v1/blocks/by_height/*",
		&protocol.RequestParams{
			PathParams:  []string{strconv.FormatUint(height, 10)},
			QueryParams: map[string][]string{"with_transactions": {"false"}},
		},
		a.configuredChain.Chain,
	)
	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return blockchain.EmptyHash, response.GetError()
	}
	var body struct {
		BlockHash string `json:"block_hash"`
	}
	if err := sonic.Unmarshal(response.ResponseResult(), &body); err != nil {
		return blockchain.EmptyHash, err
	}
	if decoded, ok := decodeHexHash(body.BlockHash); ok {
		return blockchain.NewHashIdFromBytes(decoded), nil
	}
	return blockchain.NewHashIdFromBytes(heightToBytes(height)), nil
}

func decodeHexHash(raw string) ([]byte, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "0x")
	if raw == "" {
		return nil, false
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) > 0 {
		return decoded, true
	}
	return nil, false
}

func heightToBytes(height uint64) []byte {
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[31-i] = byte(height & 0xff)
		height >>= 8
	}
	return out
}

func subOne(height uint64) uint64 {
	if height == 0 {
		return 0
	}
	return height - 1
}

var _ chains_specific.ChainSpecific = (*AptosChainSpecificObject)(nil)
