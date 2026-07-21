package ton_specific

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/ton_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/ton_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// TonV2ChainSpecificObject drives an upstream through the toncenter-style
// v2 HTTP API.
type TonV2ChainSpecificObject struct {
	tonBaseChainSpecificObject
}

func NewTonV2ChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) *TonV2ChainSpecificObject {
	return &TonV2ChainSpecificObject{
		tonBaseChainSpecificObject: newTonBaseChainSpecificObject(ctx, configuredChain, upstreamId, connector, options),
	}
}

func (t *TonV2ChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			ton_labels.NewTonClientLabelsDetector(t.configuredChain.Chain),
			t.internalTimeout,
		),
	}
	return labels.NewBaseLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.labelsDelay)
}

func (t *TonV2ChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	if t.options != nil && *t.options.DisableHealthValidation {
		return []validations.Validator[protocol.AvailabilityStatus]{}
	}
	return []validations.Validator[protocol.AvailabilityStatus]{
		ton_validations.NewTonHealthValidator(
			t.upstreamId, t.connector, t.configuredChain, t.internalTimeout,
		),
	}
}

func (t *TonV2ChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	if t.configuredChain == nil || t.configuredChain.ChainId == "" {
		return nil
	}
	if t.options != nil && *t.options.DisableChainValidation {
		return []validations.Validator[validations.ValidationSettingResult]{}
	}
	return []validations.Validator[validations.ValidationSettingResult]{
		ton_validations.NewTonChainValidator(t.upstreamId, t.connector, t.configuredChain, t.internalTimeout),
	}
}

// GetLatestBlock polls the v2 masterchain head - the masterchain is
// BFT-final, so latest == finalized.
func (t *TonV2ChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/getMasterchainInfo", nil, t.configuredChain.Chain)
	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	block, err := t.ParseBlock(response.ResponseResult())
	if err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info: %w", err)
	}
	return block, nil
}

func (t *TonV2ChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return t.GetLatestBlock(ctx)
}

// ParseBlock expects the v2 toncenter envelope {"ok":true,"result":{"last":{...}}}.
func (t *TonV2ChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	info := tonMasterchainInfoEnvelope{}
	if err := sonic.Unmarshal(blockBytes, &info); err != nil {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, reason - %s", err.Error())
	}
	if !info.Ok || info.Result.Last.RootHash == "" {
		return protocol.ZeroBlock{}, fmt.Errorf("couldn't parse the ton masterchain info, got '%s'", string(blockBytes))
	}
	return tonBlockFromIdExt(info.Result.Last), nil
}

type tonMasterchainInfoEnvelope struct {
	Ok     bool                 `json:"ok"`
	Result tonMasterchainResult `json:"result"`
}

type tonMasterchainResult struct {
	Last tonBlockIdExt `json:"last"`
}

var _ chains_specific.ChainSpecific = (*TonV2ChainSpecificObject)(nil)
