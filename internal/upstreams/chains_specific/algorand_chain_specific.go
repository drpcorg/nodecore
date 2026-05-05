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

// AlgorandChainSpecificObject wires Algorand-specific processors over the algod
// REST API. All internal calls go through [protocol.NewInternalUpstreamRestRequest]
// so the shared HttpConnector forwards them as plain GET/POSTs against the
// upstream endpoint instead of wrapping them in a JSON-RPC envelope.
type AlgorandChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func NewAlgorandChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	internalTimeout, labelsDelay time.Duration,
) *AlgorandChainSpecificObject {
	return &AlgorandChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
		labelsDelay:     labelsDelay,
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

// GetFinalizedBlock has no separate finalized tip in Algorand: with pure proof
// of stake every committed block is final once it has been certified. The
// safest approximation is therefore the latest round itself.
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
