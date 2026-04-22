package specific

import (
	"context"
	"fmt"

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
	upstreamId string
	connector  connectors.ApiConnector
}

func (a *AlgorandChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	return nil
}

func (a *AlgorandChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return nil
}

func (a *AlgorandChainSpecificObject) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	return nil
}

func (a *AlgorandChainSpecificObject) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

func (a *AlgorandChainSpecificObject) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("status", []interface{}{}, chains.ALGORAND)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return protocol.ZeroBlock{}, response.GetError()
	}

	return a.ParseBlock(response.ResponseResult())
}

func (a *AlgorandChainSpecificObject) GetFinalizedBlock(_ context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (a *AlgorandChainSpecificObject) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	status := AlgorandStatus{}
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

func NewAlgorandChainSpecificObject(
	upstreamId string,
	connector connectors.ApiConnector,
) *AlgorandChainSpecificObject {
	return &AlgorandChainSpecificObject{
		upstreamId: upstreamId,
		connector:  connector,
	}
}

type AlgorandStatus struct {
	LastRound uint64 `json:"last-round"`
}

var _ ChainSpecific = (*AlgorandChainSpecificObject)(nil)
