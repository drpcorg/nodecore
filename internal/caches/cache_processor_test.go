package caches

import (
	"bytes"
	"context"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/drpcorg/dshaltie/pkg/test_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

func TestCacheProcessorNoPoliciesThenReceiveNothing(t *testing.T) {
	cacheConfig := memoryCacheConfig(nil, nil)
	cacheProcessor := NewCacheProcessor(nil, cacheConfig, 1*time.Minute)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("method", nil)

	result, ok := cacheProcessor.Receive(context.Background(), chains.ALEPHZERO, request)

	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCacheProcessorStore(t *testing.T) {
	connector1 := test_utils.NewCacheConnectorMock()
	connector1.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	policy1 := NewCachePolicy(nil, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy1}, 1*time.Minute)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("eth_superTest", nil)

	cacheProcessor.Store(chains.POLYGON, request, []byte(`result`))

	connector1.AssertExpectations(t)
}

func TestCacheProcessorReceiveFromMatchedConditions(t *testing.T) {
	result := []byte(`result`)

	connector1 := test_utils.NewCacheConnectorMock()
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(nil, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := test_utils.NewCacheConnectorMock()
	policy2 := NewCachePolicy(nil, connector2, test_utils.PolicyConfig("ethereum|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := test_utils.NewCacheConnectorMock()
	policy3 := NewCachePolicy(nil, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 1*time.Minute)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("getLastBlock", nil)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	connector1.AssertExpectations(t)
	connector2.AssertNotCalled(t, "Receive")
	connector3.AssertNotCalled(t, "Receive")

	assert.True(t, ok)
	assert.True(t, bytes.Equal(actual, result))
}

func TestCacheProcessorNoResponseWithTimeoutThenReceiveNothing(t *testing.T) {
	result := []byte(`result`)

	connector1 := test_utils.NewDelayedConnector(50 * time.Millisecond)
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(nil, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := test_utils.NewDelayedConnector(50 * time.Millisecond)
	connector2.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy2 := NewCachePolicy(nil, connector2, test_utils.PolicyConfig("ethereum|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := test_utils.NewDelayedConnector(50 * time.Millisecond)
	connector3.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy3 := NewCachePolicy(nil, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("getLastBlock", nil)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	assert.False(t, ok)
	assert.Nil(t, actual)
}

func TestCacheProcessorReturnFirstResponseAndIgnoreOthers(t *testing.T) {
	result := []byte(`result`)

	connector1 := test_utils.NewDelayedConnector(30 * time.Millisecond)
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(nil, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := test_utils.NewDelayedConnector(0)
	connector2.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy2 := NewCachePolicy(nil, connector2, test_utils.PolicyConfig("polygon|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := test_utils.NewDelayedConnector(0)
	connector3.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy3 := NewCachePolicy(nil, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("eth_call", nil)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	time.Sleep(50 * time.Millisecond)

	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
	connector3.AssertExpectations(t)

	assert.True(t, ok)
	assert.True(t, bytes.Equal(actual, result))
}

func TestCacheProcessorNoResponseFromConnectorsThenNothing(t *testing.T) {
	connector1 := test_utils.NewDelayedConnector(0)
	connector1.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector1.On("Id").Return("id")
	policy1 := NewCachePolicy(nil, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := test_utils.NewDelayedConnector(0)
	connector2.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector2.On("Id").Return("id")
	policy2 := NewCachePolicy(nil, connector2, test_utils.PolicyConfig("polygon|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := test_utils.NewDelayedConnector(0)
	connector3.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector3.On("Id").Return("id")
	policy3 := NewCachePolicy(nil, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewInternalJsonRpcUpstreamRequest("eth_call", nil)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
	connector3.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, actual)
}

func createCacheProcessor(policies []*CachePolicy, timeout time.Duration) *CacheProcessor {
	return &CacheProcessor{
		policies:       policies,
		receiveTimeout: timeout,
	}
}

func memoryCacheConfig(connectors []*config.CacheConnectorConfig, policies []*config.CachePolicyConfig) *config.CacheConfig {
	return &config.CacheConfig{
		CacheConnectors: connectors,
		CachePolicies:   policies,
	}
}
