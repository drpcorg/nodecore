package caches_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/caches"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/drpcorg/dshaltie/pkg/test_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"os"
	"testing"
)

func TestCachePolicyNotMatchedChainThenReceiveAndStoreNothing(t *testing.T) {
	policyCfg := test_utils.PolicyConfig("not-supported", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, test_utils.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("method", nil)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyNotSupportedMethodThenReceiveAndStoreNothing(t *testing.T) {
	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, test_utils.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("method", nil)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	assert.False(t, ok)
	assert.Nil(t, result)

	ok = policy.Store(chains.POLYGON, request, []byte(`result`))

	assert.False(t, ok)
}

func TestCachePolicyIfConnectorErrorThenReceiveNothing(t *testing.T) {
	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, caches.ErrCacheNotFound)
	connectorMock.On("Id").Return("id")

	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("test_method", nil)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)

	connectorMock.AssertExpectations(t)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCachePolicyTooBigResponseSizeThenStoreNothing(t *testing.T) {
	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "1KB", "5s", true)
	policy := caches.NewCachePolicy(nil, test_utils.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("test_method", nil)

	bigResponse, _ := os.ReadFile("responses/big_response.json")

	ok := policy.Store(chains.POLYGON, request, bigResponse)

	assert.False(t, ok)
}

func TestCachePolicyNotEmptyResponsesThenStoreNothing(t *testing.T) {
	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", false)
	policy := caches.NewCachePolicy(nil, test_utils.NewCacheConnectorMock(), policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("test_method", nil)

	for _, emptyResponse := range caches.EmptyResponses {
		t.Run(fmt.Sprintf("test of emptyResponse %s", string(emptyResponse)), func(te *testing.T) {
			ok := policy.Store(chains.POLYGON, request, emptyResponse)

			assert.False(te, ok)
		})
	}
}

func TestCachePolicyStoreErrorThenFalse(t *testing.T) {
	err := errors.New("store error")
	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(err)
	connectorMock.On("Id").Return("id")

	policyCfg := test_utils.PolicyConfig("polygon", "test_method|eth_*", "conn-id", "10KB", "5s", false)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("test_method", nil)

	ok := policy.Store(chains.POLYGON, request, []byte(`result`))

	connectorMock.AssertExpectations(t)
	assert.False(t, ok)
}

func TestCachePolicyMultipleChainsThenReceiveAndStoreResultForAllOfThem(t *testing.T) {
	result1 := []byte(`result1`)
	result2 := []byte(`result2`)

	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil).Once()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result2, nil).Once()

	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("polygon|ethereum", "test_method|eth_*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("test_method", nil)

	result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
	assert.True(t, ok)
	assert.True(t, bytes.Equal(result, result1))

	result, ok = policy.Receive(context.Background(), chains.ETHEREUM, request)
	assert.True(t, ok)
	assert.True(t, bytes.Equal(result, result2))

	ok = policy.Store(chains.POLYGON, request, result1)
	assert.True(t, ok)

	ok = policy.Store(chains.ETHEREUM, request, result1)
	assert.True(t, ok)

	connectorMock.AssertExpectations(t)
}

func TestCachePolicyAnyMethodThenReceiveAndStoreResult(t *testing.T) {
	result1 := []byte(`result1`)

	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("polygon", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)

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
			request, _ := protocol.NewInternalJsonRpcUpstreamRequest(test.method, nil)

			result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
			assert.True(t, ok)
			assert.True(t, bytes.Equal(result, result1))

			ok = policy.Store(chains.POLYGON, request, result1)
			assert.True(te, ok)
		})
	}

	connectorMock.AssertExpectations(t)
}

func TestCachePolicyAllChainThenReceiveResult(t *testing.T) {
	result1 := []byte(`result1`)

	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("*", "*", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)

	for _, configuredChain := range chains.GetAllChains() {
		t.Run(fmt.Sprintf("test %s", configuredChain.Chain), func(te *testing.T) {
			request, _ := protocol.NewInternalJsonRpcUpstreamRequest("method", nil)

			result, ok := policy.Receive(context.Background(), configuredChain.Chain, request)
			assert.True(t, ok)
			assert.True(t, bytes.Equal(result, result1))

			ok = policy.Store(chains.POLYGON, request, result1)
			assert.True(te, ok)
		})
	}
}

func TestCachePolicySupportedMethodsThenReceiveResultAndStoreOrNothing(t *testing.T) {
	result1 := []byte(`result1`)

	connectorMock := test_utils.NewCacheConnectorMock()
	connectorMock.On("Receive", mock.Anything, mock.Anything).Return(result1, nil)
	connectorMock.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	policyCfg := test_utils.PolicyConfig("polygon", "eth_*|getLastBlock", "conn-id", "10KB", "5s", true)
	policy := caches.NewCachePolicy(nil, connectorMock, policyCfg)

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
			request, _ := protocol.NewInternalJsonRpcUpstreamRequest(test.method, nil)

			result, ok := policy.Receive(context.Background(), chains.POLYGON, request)
			assert.Equal(t, test.expected, ok)
			assert.Equal(t, test.expected, bytes.Equal(result, result1))

			ok = policy.Store(chains.POLYGON, request, result1)
			assert.Equal(te, test.expected, ok)
		})
	}

	connectorMock.AssertExpectations(t)
}
