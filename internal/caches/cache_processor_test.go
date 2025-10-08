package caches

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCacheProcessorNoPoliciesThenReceiveNothing(t *testing.T) {
	cacheConfig := memoryCacheConfig(nil, nil)
	cacheProcessor, err := NewBaseCacheProcessor(nil, cacheConfig)
	assert.NoError(t, err)

	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("method", nil)

	result, ok := cacheProcessor.Receive(context.Background(), chains.ALEPHZERO, request)

	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCacheProcessorStore(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()

	connector1 := mocks.NewCacheConnectorMock()
	connector1.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	policy1 := NewCachePolicy(upSupervisor, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))
	specMethod := specs.DefaultMethod("eth_superTest")

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy1}, 1*time.Minute)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_superTest", nil, specMethod)

	cacheProcessor.Store(context.Background(), chains.POLYGON, request, []byte(`result`))

	connector1.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	methodsMock.AssertExpectations(t)
}

func TestCacheProcessorReceiveFromMatchedConditions(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("getLastBlock")

	result := []byte(`result`)

	connector1 := mocks.NewCacheConnectorMock()
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(upSupervisor, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := mocks.NewCacheConnectorMock()
	policy2 := NewCachePolicy(upSupervisor, connector2, test_utils.PolicyConfig("ethereum|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := mocks.NewCacheConnectorMock()
	policy3 := NewCachePolicy(upSupervisor, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 1*time.Minute)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("getLastBlock", nil, specMethod)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	connector1.AssertExpectations(t)
	connector2.AssertNotCalled(t, "Receive")
	connector3.AssertNotCalled(t, "Receive")
	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.True(t, ok)
	assert.True(t, bytes.Equal(actual, result))
}

func TestCacheProcessorNoResponseWithTimeoutThenReceiveNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("getLastBlock")

	result := []byte(`result`)

	connector1 := mocks.NewDelayedConnector(50 * time.Millisecond)
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(upSupervisor, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := mocks.NewDelayedConnector(50 * time.Millisecond)
	connector2.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy2 := NewCachePolicy(upSupervisor, connector2, test_utils.PolicyConfig("ethereum|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := mocks.NewDelayedConnector(50 * time.Millisecond)
	connector3.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy3 := NewCachePolicy(upSupervisor, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("getLastBlock", nil, specMethod)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	methodsMock.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, actual)
}

func TestCacheProcessorReturnFirstResponseAndIgnoreOthers(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("eth_call")
	result := []byte(`result`)

	connector1 := mocks.NewDelayedConnector(30 * time.Millisecond)
	connector1.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy1 := NewCachePolicy(upSupervisor, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := mocks.NewDelayedConnector(0)
	connector2.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy2 := NewCachePolicy(upSupervisor, connector2, test_utils.PolicyConfig("polygon|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := mocks.NewDelayedConnector(0)
	connector3.On("Receive", mock.Anything, mock.Anything).Return(result, nil)
	policy3 := NewCachePolicy(upSupervisor, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_call", nil, specMethod)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	time.Sleep(50 * time.Millisecond)

	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
	connector3.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	methodsMock.AssertExpectations(t)

	assert.True(t, ok)
	assert.True(t, bytes.Equal(actual, result))
}

func TestCacheProcessorNoResponseFromConnectorsThenNothing(t *testing.T) {
	methodsMock, upSupervisor := test_utils.GetMethodMockAndUpSupervisor()
	specMethod := specs.DefaultMethod("eth_call")
	connector1 := mocks.NewDelayedConnector(0)
	connector1.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector1.On("Id").Return("id")
	policy1 := NewCachePolicy(upSupervisor, connector1, test_utils.PolicyConfig("polygon", "eth_*|getLastBlock|synscing", "conn-id", "10KB", "5s", true))

	connector2 := mocks.NewDelayedConnector(0)
	connector2.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector2.On("Id").Return("id")
	policy2 := NewCachePolicy(upSupervisor, connector2, test_utils.PolicyConfig("polygon|solana", "*", "conn-id", "10KB", "5s", true))

	connector3 := mocks.NewDelayedConnector(0)
	connector3.On("Receive", mock.Anything, mock.Anything).Return([]byte{}, ErrCacheNotFound)
	connector3.On("Id").Return("id")
	policy3 := NewCachePolicy(upSupervisor, connector3, test_utils.PolicyConfig("gnosis|polygon", "eth_call", "conn-id", "10KB", "5s", true))

	cacheProcessor := createCacheProcessor([]*CachePolicy{policy2, policy3, policy1}, 10*time.Millisecond)
	request, _ := protocol.NewUpstreamJsonRpcRequestWithSpecMethod("eth_call", nil, specMethod)

	actual, ok := cacheProcessor.Receive(context.Background(), chains.POLYGON, request)

	connector1.AssertExpectations(t)
	connector2.AssertExpectations(t)
	connector3.AssertExpectations(t)
	upSupervisor.AssertExpectations(t)
	methodsMock.AssertExpectations(t)

	assert.False(t, ok)
	assert.Nil(t, actual)
}

func createCacheProcessor(policies []*CachePolicy, timeout time.Duration) CacheProcessor {
	return &BaseCacheProcessor{
		policies:       policies,
		receiveTimeout: timeout,
	}
}

func memoryCacheConfig(connectors []*config.CacheConnectorConfig, policies []*config.CachePolicyConfig) *config.CacheConfig {
	return &config.CacheConfig{
		ReceiveTimeout:  1 * time.Second,
		CacheConnectors: connectors,
		CachePolicies:   policies,
	}
}
