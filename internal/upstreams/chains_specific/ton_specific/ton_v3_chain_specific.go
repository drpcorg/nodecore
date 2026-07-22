package ton_specific

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
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ton_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ton_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// TonV3ChainSpecificObject drives an upstream through the v3 indexer API.
type TonV3ChainSpecificObject struct {
	tonBaseChainSpecificObject
}

func NewTonV3ChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) *TonV3ChainSpecificObject {
	return &TonV3ChainSpecificObject{
		tonBaseChainSpecificObject: newTonBaseChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options),
	}
}

func (t *TonV3ChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return t.newTonBlockProcessor(t)
}

func (t *TonV3ChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			ton_labels.NewTonV3ClientLabelsDetector(t.configuredChain.Chain),
			t.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.labelsDelay)
}

func (t *TonV3ChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if t.options != nil && *t.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		ton_validations.NewTonV3SyncingValidator(
			t.upstreamId, t.connector, t.configuredChain, t.internalTimeout,
		),
	}
}

func (t *TonV3ChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if t.configuredChain == nil || t.configuredChain.ChainId == "" {
		return nil
	}
	if t.options != nil && *t.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		ton_validations.NewTonV3ChainValidator(t.upstreamId, t.connector, t.configuredChain, t.internalTimeout),
	}
}

// GetLatestBlock polls the v3 masterchain head - the masterchain is
// BFT-final, so latest == finalized.
func (t *TonV3ChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/api/v3/masterchainInfo", nil, t.configuredChain.Chain)
	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := t.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton v3 masterchain info: %w", err)
	}
	return block, nil
}

func (t *TonV3ChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return t.GetLatestBlock(ctx)
}

// ParseBlock expects the bare v3 shape {"last":{...}}.
func (t *TonV3ChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	info := tonV3MasterchainInfo{}
	if err := sonic.Unmarshal(blockBytes, &info); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton v3 masterchain info, reason - %s", err.Error())
	}
	if info.Last.RootHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton v3 masterchain info, got '%s'", string(blockBytes))
	}
	return tonBlockFromIdExt(info.Last), nil
}

type tonV3MasterchainInfo struct {
	Last tonBlockIdExt `json:"last"`
}

var _ chains_specific.ChainSpecific = (*TonV3ChainSpecificObject)(nil)
