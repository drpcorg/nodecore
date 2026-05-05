package specific

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/config"
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
	options         *config.UpstreamOptions
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewAlgorandChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *config.UpstreamOptions,
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
	request := protocol.NewInternalUpstreamRestRequest("GET", "/v2/status", a.configuredChain.Chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AlgorandChainSpecificObject) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	return a.GetLatestBlock(ctx)
}

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

var _ ChainSpecific = (*AlgorandChainSpecificObject)(nil)
