package flow

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStrategyExec(t *testing.T, upstreamConfig *config.UpstreamConfig) *BaseExecutionFlow {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	chSup := test_utils.CreateChainSupervisor()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(chSup)

	registry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})

	return &BaseExecutionFlow{
		chain:              chains.ETHEREUM,
		upstreamSupervisor: upSupervisor,
		registry:           registry,
		appConfig:          &config.AppConfig{UpstreamConfig: upstreamConfig},
	}
}

func TestCreateStrategyDefaultsToRating(t *testing.T) {
	exec := newStrategyExec(t, &config.UpstreamConfig{})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}, false, "eth")

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
}

func TestCreateStrategyUsesBaseWhenConfigured(t *testing.T) {
	exec := newStrategyExec(t, &config.UpstreamConfig{BalancingStrategy: config.BaseBalancingStrategy})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}, false, "eth")

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &BaseStrategy{}, strategy)
}

func TestCreateStrategyPerChainOverrideWinsOverGlobal(t *testing.T) {
	exec := newStrategyExec(t, &config.UpstreamConfig{
		BalancingStrategy: config.BaseBalancingStrategy,
		ChainDefaults: map[string]*config.ChainDefaults{
			chains.ETHEREUM.String(): {BalancingStrategy: config.RatingBalancingStrategy},
		},
	})
	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_call"}, false, "eth")

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
}
