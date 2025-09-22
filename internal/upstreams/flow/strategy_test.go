package flow_test

import (
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestRatingStrategyWithUpstreamIndexMatcherNotExist(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)

	ratingRegistry := rating.NewRatingRegistry(upSupervisor, nil, &config.ScorePolicyConfig{CalculationFunctionName: config.DefaultLatencyPolicyFuncName, CalculationInterval: 1 * time.Minute})

	additionalMatchers := []flow.Matcher{flow.NewUpstreamIndexMatcher("notExist")}
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil)
	ratingStrategy := flow.NewRatingStrategy(chains.ARBITRUM, "eth_getBalance", additionalMatchers, chSup, ratingRegistry)

	_, err := ratingStrategy.SelectUpstream(request)

	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestRatingStrategyGetBestByLatency(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id2", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id3", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id4", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id5", protocol.Unavailable, mapset.NewThreadUnsafeSet[protocol.Cap]())

	additionalMatchers := []flow.Matcher{flow.NewUpstreamIndexMatcher("index")}
	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisors").Return([]*upstreams.ChainSupervisor{chSup})

	tracker := dimensions.NewDimensionTracker()
	dims1 := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id1", "eth_getBalance")
	dims1.TrackRequestDuration(1000000)
	dims2 := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id2", "eth_getBalance")
	dims2.TrackRequestDuration(4000000)
	dims3 := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id3", "eth_getBalance")
	dims3.TrackRequestDuration(500000)
	dims4 := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id4", "eth_getBalance")
	dims4.TrackRequestDuration(360000)
	dims5 := tracker.GetUpstreamDimensions(chains.ARBITRUM, "id5", "eth_getBalance")
	dims5.TrackRequestDuration(8000000)

	ratingRegistry := rating.NewRatingRegistry(upSupervisor, tracker, &config.ScorePolicyConfig{CalculationFunctionName: config.DefaultLatencyPolicyFuncName, CalculationInterval: 1 * time.Minute})
	go ratingRegistry.Start()
	time.Sleep(10 * time.Millisecond)

	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil)
	ratingStrategy := flow.NewRatingStrategy(chains.ARBITRUM, "eth_getBalance", additionalMatchers, chSup, ratingRegistry)

	upSupervisor.AssertExpectations(t)

	up, err := ratingStrategy.SelectUpstream(request)
	assert.Nil(t, err)
	assert.Equal(t, "id4", up)

	up, err = ratingStrategy.SelectUpstream(request)
	assert.Nil(t, err)
	assert.Equal(t, "id3", up)

	up, err = ratingStrategy.SelectUpstream(request)
	assert.Nil(t, err)
	assert.Equal(t, "id1", up)

	up, err = ratingStrategy.SelectUpstream(request)
	assert.Nil(t, err)
	assert.Equal(t, "id2", up)

	_, err = ratingStrategy.SelectUpstream(request)
	assert.NotNil(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestRatingStrategyMatchersErrors(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		publishEventFunc func(chSup *upstreams.ChainSupervisor)
		requestFunc      func(method string) protocol.RequestHolder
		expectedErr      error
	}{
		{
			name:   "no available upstreams if no events",
			method: "eth_getBalance",
			requestFunc: func(method string) protocol.RequestHolder {
				return nil
			},
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
			},
			expectedErr: protocol.NoAvailableUpstreamsError(),
		},
		{
			name:   "no available upstreams",
			method: "eth_getBalance",
			requestFunc: func(method string) protocol.RequestHolder {
				request, _ := protocol.NewInternalUpstreamJsonRpcRequest(method, nil)
				return request
			},
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Unavailable, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			expectedErr: protocol.NoAvailableUpstreamsError(),
		},
		{
			name:   "no available method",
			method: "test",
			requestFunc: func(method string) protocol.RequestHolder {
				request, _ := protocol.NewInternalUpstreamJsonRpcRequest(method, nil)
				return request
			},
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			expectedErr: protocol.NotSupportedMethodError("test"),
		},
		{
			name:   "no available sub method",
			method: "eth_getBalance",
			requestFunc: func(method string) protocol.RequestHolder {
				request := protocol.NewUpstreamJsonRpcRequest("id", []byte("1"), "eth_getBalance", nil, true, nil)
				return request
			},
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			expectedErr: protocol.NotSupportedMethodError("eth_getBalance"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			chSup := test_utils.CreateChainSupervisor()
			test.publishEventFunc(chSup)

			upSupervisor := mocks.NewUpstreamSupervisorMock()
			upSupervisor.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)

			tracker := dimensions.NewDimensionTracker()
			ratingRegistry := rating.NewRatingRegistry(upSupervisor, tracker, &config.ScorePolicyConfig{CalculationFunctionName: config.DefaultLatencyPolicyFuncName})

			request := test.requestFunc(test.method)

			ratingStrategy := flow.NewRatingStrategy(chains.ARBITRUM, test.method, nil, chSup, ratingRegistry)

			_, err := ratingStrategy.SelectUpstream(request)

			upSupervisor.AssertExpectations(t)

			assert.NotNil(t, err)
			assert.Equal(t, test.expectedErr, err)
		})
	}
}

func TestBaseStrategyMatchersErrors(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		publishEventFunc func(chSup *upstreams.ChainSupervisor)
		requestFunc      func(method string) protocol.RequestHolder
		expectedErr      error
	}{
		{
			name:   "no available upstreams if no events",
			method: "eth_getBalance",
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
			},
			requestFunc: func(method string) protocol.RequestHolder {
				return nil
			},
			expectedErr: protocol.NoAvailableUpstreamsError(),
		},
		{
			name:   "no available sub method",
			method: "eth_getBalance",
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			requestFunc: func(method string) protocol.RequestHolder {
				request := protocol.NewUpstreamJsonRpcRequest("id", []byte("1"), "eth_getBalance", nil, true, nil)
				return request
			},
			expectedErr: protocol.NotSupportedMethodError("eth_getBalance"),
		},
		{
			name:   "no available upstreams",
			method: "eth_getBalance",
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Unavailable, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			requestFunc: func(method string) protocol.RequestHolder {
				request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil)
				return request
			},
			expectedErr: protocol.NoAvailableUpstreamsError(),
		},
		{
			name:   "no supported method",
			method: "test",
			publishEventFunc: func(chSup *upstreams.ChainSupervisor) {
				test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
			},
			requestFunc: func(method string) protocol.RequestHolder {
				request, _ := protocol.NewInternalUpstreamJsonRpcRequest("test", nil)
				return request
			},
			expectedErr: protocol.NotSupportedMethodError("test"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			chSup := test_utils.CreateChainSupervisor()
			test.publishEventFunc(chSup)

			request := test.requestFunc(test.method)
			baseStrategy := flow.NewBaseStrategy(chSup)

			_, err := baseStrategy.SelectUpstream(request)

			assert.NotNil(t, err)
			assert.Equal(t, test.expectedErr, err)
		})
	}
}

func TestBaseStrategyWithWsCap(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))
	request := protocol.NewUpstreamJsonRpcRequest("id", []byte("1"), "eth_getBalance", nil, true, nil)
	baseStrategy := flow.NewBaseStrategy(chSup)

	upId, err := baseStrategy.SelectUpstream(request)

	assert.Nil(t, err)
	assert.Equal(t, "id1", upId)
}

func TestBaseStrategyGetUpstreams(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id2", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil)
	baseStrategy := flow.NewBaseStrategy(chSup)

	upId, err := baseStrategy.SelectUpstream(request)

	assert.Nil(t, err)
	assert.Equal(t, "id2", upId)

	upId, err = baseStrategy.SelectUpstream(request)

	assert.Nil(t, err)
	assert.Equal(t, "id1", upId)

	_, err = baseStrategy.SelectUpstream(request)

	assert.NotNil(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}
