package flow

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
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

// newLabelBalancedExec builds a flow for a chain under label-balancing that a
// request can only escape by skipping it: only "full" is in the order, the
// default group is off and a retry jumps to the next group.
func newLabelBalancedExec(t *testing.T, balancingStrategy config.BalancingStrategy) *GenericExecutionFlow {
	t.Helper()
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ETHEREUM).Return(chSup)
	for id, labels := range map[string][]string{"full-up": {"full"}, "archive-up": {"archive"}, "plain-up": nil} {
		test_utils.PublishEvent(chSup, id, protocol.Available, mapset.NewThreadUnsafeSet(protocol.WsCap))
		upSupervisor.On("GetUpstream", id).Return(
			upstreams.NewGenericUpstreamWithParams(id, chains.ETHEREUM, nil, &config.Upstream{Id: id, GroupLabels: labels}, "", nil, nil, nil, nil, false),
		)
	}
	registry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     1 * time.Minute,
	})

	return &GenericExecutionFlow{
		chain:              chains.ETHEREUM,
		upstreamSupervisor: upSupervisor,
		registry:           registry,
		appConfig: &config.AppConfig{UpstreamConfig: &config.UpstreamConfig{
			BalancingStrategy: balancingStrategy,
			LabelBalancing: &config.LabelBalancingConfig{
				Order:          []string{"full"},
				PassOnError:    true,
				IncludeDefault: new(false),
			},
		}},
	}
}

func routedRequest(isSub bool, selectors ...protocol.RequestSelector) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_getBalance"}, isSub, "eth", selectors...)
}

func upstreamIdSelector(values ...string) protocol.RequestUpstreamIdSelector {
	return protocol.RequestUpstreamIdSelector{Ids: mapset.NewThreadUnsafeSet(values...)}
}

var headerArchive = protocol.RequestGroupLabelSelector{Labels: []string{"archive"}, FromRoutingHeader: true}

// plain-up carries no order label and include-default is off: label-balancing
// would never reach it, the id header must
func TestCreateStrategyUpstreamIdSkipsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, upstreamIdSelector("plain-up"))

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
	upstreamId, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "plain-up", upstreamId)
	_, err = strategy.SelectUpstream(request) // a retry has nowhere else to go
	assert.Error(t, err)
}

// newLabelBalancedExec publishes every upstream with this index
const labelBalancedIndex = "index"

func upstreamSelector(id, index string) protocol.RequestUpstreamSelector {
	return protocol.RequestUpstreamSelector{Id: id, Index: index}
}

// plain-up is outside the label order and the default group is off: only a
// request that skips label-balancing reaches it
func TestCreateStrategyUpstreamSelectorSkipsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, upstreamSelector("plain-up", labelBalancedIndex))

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
	upstreamId, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "plain-up", upstreamId)
	_, err = strategy.SelectUpstream(request) // a retry has nowhere else to go
	assert.Error(t, err)
}

func TestCreateStrategyUpstreamSelectorUnderAndSkipsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, protocol.RequestAndSelector{Children: []protocol.RequestSelector{
		protocol.RequestAndSelector{Children: []protocol.RequestSelector{upstreamSelector("plain-up", labelBalancedIndex)}},
	}})

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
	upstreamId, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "plain-up", upstreamId)
	_, err = strategy.SelectUpstream(request)
	assert.Error(t, err)
}

// under OR the selector no longer names one upstream, so the request stays
// under label-balancing
func TestCreateStrategyUpstreamSelectorUnderOrKeepsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, protocol.RequestOrSelector{Children: []protocol.RequestSelector{
		upstreamSelector("plain-up", labelBalancedIndex),
		upstreamSelector("full-up", labelBalancedIndex),
	}})

	assert.IsType(t, &LabelGroupStrategy{}, exec.createStrategy(context.Background(), request))
}

// the index of a replaced upstream no longer matches: nothing else is tried
func TestCreateStrategyUpstreamSelectorWrongIndexHasNoUpstream(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, upstreamSelector("plain-up", "00042"))

	_, err := exec.createStrategy(context.Background(), request).SelectUpstream(request)

	assert.Error(t, err)
}

func TestCreateStrategyUpstreamIdUsesBaseWhenConfigured(t *testing.T) {
	exec := newLabelBalancedExec(t, config.BaseBalancingStrategy)
	request := routedRequest(false, upstreamIdSelector("archive-up", "plain-up"))

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &GenericStrategy{}, strategy)
	first, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	second, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"archive-up", "plain-up"}, []string{first, second})
	_, err = strategy.SelectUpstream(request)
	assert.Error(t, err)
}

// pass-on-error and a label outside `order` must not get in the header's way
func TestCreateStrategyGroupHeaderSkipsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, headerArchive)

	strategy := exec.createStrategy(context.Background(), request)

	assert.IsType(t, &RatingStrategy{}, strategy)
	upstreamId, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "archive-up", upstreamId)
}

// an API key's restriction alone is still routed by label-balancing
func TestCreateStrategyKeyGroupRestrictionKeepsLabelBalancing(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	request := routedRequest(false, protocol.RequestGroupLabelSelector{Labels: []string{"archive"}})

	assert.IsType(t, &LabelGroupStrategy{}, exec.createStrategy(context.Background(), request))
}

func TestCreateStrategyRoutingSelectorsCombine(t *testing.T) {
	exec := newLabelBalancedExec(t, "")
	keyFull := protocol.RequestGroupLabelSelector{Labels: []string{"full"}}
	tests := []struct {
		name      string
		selectors []protocol.RequestSelector
		expected  string // "" = no upstream
	}{
		{"group header and matching id", []protocol.RequestSelector{headerArchive, upstreamIdSelector("archive-up")}, "archive-up"},
		{"group header and id outside the group", []protocol.RequestSelector{headerArchive, upstreamIdSelector("plain-up")}, ""},
		{"key restriction and id inside it", []protocol.RequestSelector{keyFull, upstreamIdSelector("full-up")}, "full-up"},
		{"key restriction and id outside it", []protocol.RequestSelector{keyFull, upstreamIdSelector("plain-up")}, ""},
		{"unknown id", []protocol.RequestSelector{upstreamIdSelector("no-such-upstream")}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			request := routedRequest(false, test.selectors...)
			upstreamId, err := exec.createStrategy(context.Background(), request).SelectUpstream(request)
			if test.expected == "" {
				assert.Error(te, err)
				return
			}
			assert.NoError(te, err)
			assert.Equal(te, test.expected, upstreamId)
		})
	}
}

// the branches ahead of the label-balancing check keep their place
func TestCreateStrategyRoutingSelectorsLeaveEarlierBranchesAlone(t *testing.T) {
	exec := newLabelBalancedExec(t, "")

	subscription := routedRequest(true, upstreamIdSelector("plain-up"))
	subStrategy := exec.createStrategy(context.Background(), subscription)
	assert.IsType(t, &GenericStrategy{}, subStrategy)
	upstreamId, err := subStrategy.SelectUpstream(subscription)
	assert.NoError(t, err)
	assert.Equal(t, "plain-up", upstreamId)

	// no drpc upstream on the chain: quorum must still answer for itself
	quorumCtx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	assert.IsType(t, &FailingStrategy{}, exec.createStrategy(quorumCtx, routedRequest(false, upstreamIdSelector("plain-up"))))
}
