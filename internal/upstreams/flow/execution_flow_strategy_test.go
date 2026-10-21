package flow

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
)

func newStrategyExec(t *testing.T, upstreamConfig *config.UpstreamConfig) *GenericExecutionFlow {
	t.Helper()
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(chSup)

	registry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})

	return &GenericExecutionFlow{
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

	assert.IsType(t, &GenericStrategy{}, strategy)
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

// An API key's upstream restriction arrives as a group-label selector: every
// strategy must serve the request only from upstreams carrying one of the
// labels, for plain requests and subscriptions alike.
func TestCreateStrategyHonorsGroupLabelRestriction(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(chSup)
	for id, labels := range map[string][]string{"archive-up": {"archive"}, "full-up": {"full"}, "plain-up": nil} {
		test_utils.PublishEvent(chSup, id, protocol.Available, mapset.NewThreadUnsafeSet(protocol.WsCap))
		upSupervisor.On("GetUpstream", id).Return(
			upstreams.NewGenericUpstreamWithParams(id, chains.ETHEREUM, nil, &config.Upstream{Id: id, GroupLabels: labels}, "", nil, nil, nil, nil, false),
		)
	}
	registry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})
	exec := &GenericExecutionFlow{
		chain:              chains.ETHEREUM,
		upstreamSupervisor: upSupervisor,
		registry:           registry,
		appConfig:          &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{BalancingStrategy: config.BaseBalancingStrategy}},
	}
	restriction := protocol.RequestGroupLabelSelector{Labels: []string{"archive"}}

	// createStrategy branches on IsSubscribe only, so the subscription case
	// reuses the one method the published upstream state supports.
	for name, isSub := range map[string]bool{"request": false, "subscription": true} {
		request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getBalance"}, isSub, "eth", restriction)
		t.Run(name, func(t *testing.T) {
			// A fresh strategy per attempt: base balancing round-robins, so
			// repeated picks would reach every upstream without the restriction.
			for range 5 {
				upstreamId, err := exec.createStrategy(context.Background(), request).SelectUpstream(request)
				assert.NoError(t, err)
				assert.Equal(t, "archive-up", upstreamId)
			}
			// Once the only admitted upstream is used, the request has nowhere
			// else to go - it is never retried on an excluded upstream.
			strategy := exec.createStrategy(context.Background(), request)
			_, err := strategy.SelectUpstream(request)
			assert.NoError(t, err)
			_, err = strategy.SelectUpstream(request)
			assert.Error(t, err)
		})
	}
}
