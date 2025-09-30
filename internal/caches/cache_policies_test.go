package caches_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCachePolicyNoMethodThenReceiveAndStoreNothing(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	specMethod := specs.MethodWithSettings("eth_superTest", &specs.MethodSettings{Cacheable: lo.ToPtr(false)}, nil)
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(10 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	policyCfg := test_utils.PolicyConfig("polygon", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyNotCachableMethodThenReceiveAndStoreNothing(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(10 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	policyCfg := test_utils.PolicyConfig("polygon", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("method", nil)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyFinalizedNoMatchedOrBlockTagThenReceiveAndStoreNothing(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		params    []byte
		tagParser specs.TagParser
	}{
		// eth_call tests
		{
			"eth_call with latest",
			"eth_call",
			[]byte(`[false, "latest"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		{
			"eth_call with safe",
			"eth_call",
			[]byte(`[false, "safe"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		{
			"eth_call with finalized",
			"eth_call",
			[]byte(`[false, "finalized"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		{
			"eth_call with pending",
			"eth_call",
			[]byte(`[false, "pending"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		{
			"eth_call with earliest",
			"eth_call",
			[]byte(`[false, "earliest"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		{
			"eth_call num is not finalized",
			"eth_call",
			[]byte(`[false, "0x81d9d5b"]`),
			specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"},
		},
		// eth_getLogs tests
		{
			"eth_getLogs with latest toBlock",
			"eth_getLogs",
			[]byte(`[{"toBlock": "latest"}]`),
			specs.TagParser{ReturnType: specs.ObjectType, Path: ".[0] | if .blockHash then {blockRef: .blockHash} else {blockRange: {from: .fromBlock, to: .toBlock}} end"},
		},
		{
			"eth_getLogs with finalized fromBlock",
			"eth_getLogs",
			[]byte(`[{"fromBlock": "finalized"}]`),
			specs.TagParser{ReturnType: specs.ObjectType, Path: ".[0] | if .blockHash then {blockRef: .blockHash} else {blockRange: {from: .fromBlock, to: .toBlock}} end"},
		},
	}

	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet("eth_superTest"))

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(1000), protocol.FinalizedBlock)

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, 100, methodsMock, blockInfo1))
	time.Sleep(10 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			connectorMock := mocks.NewCacheConnectorMock()
			connectorMock.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, caches.ErrCacheNotFound)
			connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			connectorMock.On("Id").Return("test-connector")

			policyCfg := test_utils.PolicyConfigFinalized("polygon", "*", "conn-id", "10KB", "5s", true)
			policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)

			method := specs.MethodWithSettings(test.method, &specs.MethodSettings{Cacheable: lo.ToPtr(true)}, &test.tagParser)
			request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), test.method, test.params, false, method)

			result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

			assert.False(te, ok)
			assert.Nil(te, result)

			ok = policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

			assert.False(te, ok)
		})
	}
}

func TestCachePolicyNotMatchedChainThenReceiveAndStoreNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()

	policyCfg := test_utils.PolicyConfig("not-supported", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	specMethod := specs.DefaultMethod("method")
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyNotSupportedMethodThenReceiveAndStoreNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()

	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	specMethod := specs.DefaultMethod("method")
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyIfConnectorErrorThenReceiveNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, caches.ErrCacheNotFound)
	connectorMock.On("Id").Return("id")
	specMethod := specs.DefaultMethod("test_method")

	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("test_method", nil, specMethod)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	connectorMock.AssertExpectations(t)
	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCachePolicyTooBigResponseSizeThenStoreNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "1KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	specMethod := specs.DefaultMethod("test_method")
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("test_method", nil, specMethod)

	bigResponse, _ := os.ReadFile("responses/big_response.json")

	ok := policy.Store(context.Background(), chains.POLYGON, request, bigResponse)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	assert.False(t, ok)
}

func TestCachePolicyNotEmptyResponsesThenStoreNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", false)
	policy := caches.NewCachePolicy(upSupervisor, mocks.NewCacheConnectorMock(), policyCfg)
	specMethod := specs.DefaultMethod("test_method")
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("test_method", nil, specMethod)

	for _, emptyResponse := range caches.EmptyResponses {
		t.Run(fmt.Sprintf("test of emptyResponse %s", string(emptyResponse)), func(te *testing.T) {
			ok := policy.Store(context.Background(), chains.POLYGON, request, emptyResponse)

			methodsMock.AssertExpectations(t)
			upSupervisor.AssertExpectations(t)
			assert.False(te, ok)
		})
	}
}

func TestCachePolicyStoreErrorThenFalse(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("test_method")
	err := errors.New("store error")
	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(err)
	connectorMock.On("Id").Return("id")

	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", false)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("test_method", nil, specMethod)

	ok := policy.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connectorMock.AssertExpectations(t)
	assert.False(t, ok)
}

func TestCachePolicyMultipleChainsThenReceiveAndStoreResultForAllOfThem(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("test_method")
	result1 := []byte(`result1`)
	result2 := []byte(`result2`)

	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil).Once()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result2, nil).Once()

	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("polygon|ethereum", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("test_method", nil, specMethod)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
	assert.True(t, ok)
	assert.True(t, bytes.Equal(result, result1))

	result, ok = policy.Receive(context.Background(), chains.ETHEREUM, request)
	assert.True(t, ok)
	assert.True(t, bytes.Equal(result, result2))

	ok = policy.Store(context.Background(), chains.POLYGON, request, result1)
	assert.True(t, ok)

	ok = policy.Store(context.Background(), chains.ETHEREUM, request, result1)
	assert.True(t, ok)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connectorMock.AssertExpectations(t)
}

func TestCachePolicyAnyMethodThenReceiveAndStoreResult(t *testing.T) {
	tagParser := specs.TagParser{ReturnType: specs.BlockNumberType, Path: ".[1]"}
	method := specs.MethodWithSettings("eth_call", &specs.MethodSettings{Cacheable: lo.ToPtr(true)}, &tagParser)

	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	blockInfo1 := protocol.NewBlockInfo()
	blockInfo1.AddBlock(protocol.NewBlockDataWithHeight(1000), protocol.FinalizedBlock)

	go chainSupervisor.Start()

	chainSupervisor.Publish(test_utils.CreateEventWithBlockData("id", protocol.Available, 100, methodsMock, blockInfo1))
	time.Sleep(10 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	result1 := []byte(`result1`)

	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfigFinalized("polygon", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)

	tests := []struct {
		name   string
		method string
	}{
		{
			name:   "method #1",
			method: "firstMethod",
		},
		{
			name:   "method #2",
			method: "another_one",
		},
		{
			name:   "method #3",
			method: "eth_call",
		},
		{
			name:   "method #4",
			method: "getLastBlock",
		},
		{
			name:   "method #5",
			method: "anyMethod",
		},
		{
			name:   "method #6",
			method: "next_one",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", []byte(`[false, "0x3"]`), false, method)

			result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
			assert.True(t, ok)
			assert.True(t, bytes.Equal(result, result1))

			ok = policy.Store(context.Background(), chains.POLYGON, request, result1)
			assert.True(te, ok)
		})
	}

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connectorMock.AssertExpectations(t)
}

func TestCachePolicyAllChainThenReceiveResult(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	result1 := []byte(`result1`)
	specMethod := specs.DefaultMethod("method")

	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("*", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)

	for _, configuredChain := range chains.GetAllChains() {
		t.Run(fmt.Sprintf("test %s", configuredChain.Chain), func(te *testing.T) {
			request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("method", nil, specMethod)

			result, ok := policy.Receive(context.Background(), configuredChain.Chain, request)
			assert.True(t, ok)
			assert.True(t, bytes.Equal(result, result1))

			ok = policy.Store(context.Background(), chains.POLYGON, request, result1)

			methodsMock.AssertExpectations(t)
			upSupervisor.AssertExpectations(t)
			assert.True(te, ok)
		})
	}
}

func TestCachePolicySupportedMethodsThenReceiveResultAndStoreOrNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	result1 := []byte(`result1`)

	connectorMock := mocks.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("polygon", "eth_*|getLastBlock", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(upSupervisor, connectorMock, policyCfg)

	tests := []struct {
		name     string
		method   string
		expected bool
	}{
		{
			name:     "method #1",
			method:   "firstMethod",
			expected: false,
		},
		{
			name:     "method #2",
			method:   "another_one",
			expected: false,
		},
		{
			name:     "method #3",
			method:   "eth_call",
			expected: true,
		},
		{
			name:     "method #4",
			method:   "getLastBlock",
			expected: true,
		},
		{
			name:     "method #5",
			method:   "anyMethod",
			expected: false,
		},
		{
			name:     "method #6",
			method:   "next_one",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			specMethod := specs.DefaultMethod(test.method)
			request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod(test.method, nil, specMethod)

			result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
			assert.Equal(t, test.expected, ok)
			assert.Equal(t, test.expected, bytes.Equal(result, result1))

			ok = policy.Store(context.Background(), chains.POLYGON, request, result1)
			assert.Equal(te, test.expected, ok)
		})
	}

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	connectorMock.AssertExpectations(t)
}
