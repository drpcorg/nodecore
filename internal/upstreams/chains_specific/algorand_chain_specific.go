package specific

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
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

type AlgorandChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewAlgorandChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *AlgorandChainSpecificObject {
	return &AlgorandChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (a *AlgorandChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			a.upstreamId,
			a.connector,
			labels.NewAlgorandClientLabelsDetector(a.configuredChain.Chain),
			a.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(a.ctx, a.upstreamId, labelsDetectors, a.labelsDelay)
}

func (a *AlgorandChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		lower_bounds.NewAlgorandLowerBoundDetector(
			a.upstreamId,
			a.configuredChain.Chain,
			a.internalTimeout,
			a.connector,
		),
	}
	return lower_bounds.NewBaseLowerBoundProcessor(
		a.ctx,
		a.upstreamId,
		a.configuredChain.AverageRemoveSpeed(),
		detectors,
	)
}

func (a *AlgorandChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if a.options != nil && *a.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		validations.NewAlgorandHealthValidator(
			a.upstreamId, a.connector, a.configuredChain.Chain, a.internalTimeout,
		),
	}
}

func (a *AlgorandChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if a.configuredChain == nil || a.configuredChain.ChainId == "" {
		return nil
	}
	if a.options != nil && *a.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		validations.NewAlgorandChainValidator(a.upstreamId, a.connector, a.configuredChain, a.internalTimeout),
	}
}

func (a *AlgorandChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	statusReq := protocol.NewInternalUpstreamRestRequest("GET", "/v2/status", a.configuredChain.Chain)
	statusResp := a.connector.SendRequest(ctx, statusReq)
	if statusResp.HasError() {
		return protocol.ZeroBlock{}, statusResp.GetError()
	}
	var status validations.AlgorandStatus
	if err := sonic.Unmarshal(statusResp.ResponseResult(), &status); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse algorand status: %w", err)
	}
	if status.LastRound == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("algorand upstream '%s' has no last-round", a.upstreamId)
	}

	// Fetch the block header to extract hash + parent hash. Without this the
	// HeadEvent emitted to subscribers would carry empty BlockId /
	// ParentBlockId fields, since /v2/status only carries the round number.
	block, err := a.fetchBlockHeader(ctx, status.LastRound)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't fetch algorand block %d header: %w", status.LastRound, err)
	}

	return protocol.NewBlock(
		status.LastRound,
		0,
		decodeAlgorandHash(block.Seed, block.Txn, status.LastRound),
		decodeAlgorandHash(block.Prev, "", subOne(status.LastRound)),
	), nil
}

func (a *AlgorandChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return a.GetLatestBlock(ctx)
}

// ParseBlock keeps the legacy shape - some callers feed the raw /v2/status
// payload here and expect just height back. The hash-aware path lives in
// GetLatestBlock above.
func (a *AlgorandChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	status := validations.AlgorandStatus{}
	err := sonic.Unmarshal(blockBytes, &status)
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the algorand status, reason - %s", err.Error())
	}

	height := status.LastRound
	if height == 0 {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the algorand status, got '%s'", string(blockBytes))
	}

	return protocol.NewBlock(height, 0, blockchain.EmptyHash, blockchain.EmptyHash), nil
}

func (a *AlgorandChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, fmt.Errorf("algorand does not support websocket subscriptions")
}

func (a *AlgorandChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, fmt.Errorf("algorand does not support websocket subscriptions")
}

func (a *AlgorandChainSpecificObject) fetchBlockHeader(ctx context.Context, round uint64) (*algorandBlockHeader, error) {
	path := fmt.Sprintf("/v2/blocks/%d?header-only=true", round)
	request := protocol.NewInternalUpstreamRestRequest("GET", path, a.configuredChain.Chain)
	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	var wrapper algorandBlockResponse
	if err := sonic.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	return &wrapper.Block, nil
}

type algorandBlockResponse struct {
	Block algorandBlockHeader `json:"block"`
}

type algorandBlockHeader struct {
	Round uint64 `json:"rnd"`
	Prev  string `json:"prev"`
	Seed  string `json:"seed"`
	Txn   string `json:"txn"`
}

// decodeAlgorandHash maps an algod block-header hash field (32-byte value
// base64-encoded, sometimes prefixed with "blk-") to a 32-byte HashId. Falls
// back to a deterministic round encoding so downstream consumers always
// receive a fixed-width identifier instead of an empty hash.
func decodeAlgorandHash(primary, secondary string, round uint64) blockchain.HashId {
	for _, raw := range []string{primary, secondary} {
		if decoded, ok := tryBase64Decode(raw); ok && len(decoded) > 0 {
			return blockchain.NewHashIdFromBytes(decoded)
		}
	}
	return blockchain.NewHashIdFromBytes(roundToBytes(round))
}

func tryBase64Decode(raw string) ([]byte, bool) {
	raw = strings.TrimPrefix(raw, "blk-")
	if raw == "" {
		return nil, false
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return decoded, true
	}
	if decoded, err := base64.URLEncoding.DecodeString(raw); err == nil {
		return decoded, true
	}
	return nil, false
}

func roundToBytes(round uint64) []byte {
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[31-i] = byte(round & 0xff)
		round >>= 8
	}
	return out
}

func subOne(round uint64) uint64 {
	if round == 0 {
		return 0
	}
	return round - 1
}

var _ ChainSpecific = (*AlgorandChainSpecificObject)(nil)
