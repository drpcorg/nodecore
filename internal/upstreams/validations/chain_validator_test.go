package validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestChainValidatorChaiIdErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &config.UpstreamOptions{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)

	connector.On("SendRequest", mock.Anything, chainIdRequest).
		Return(protocol.NewTotalFailure(chainIdRequest, protocol.RequestTimeoutError()))
	connector.On("SendRequest", mock.Anything, netVersionRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`result`), protocol.JsonRpc))

	validator := validations.NewChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorNetVersionErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &config.UpstreamOptions{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)

	connector.On("SendRequest", mock.Anything, netVersionRequest).
		Return(protocol.NewTotalFailure(netVersionRequest, protocol.RequestTimeoutError()))
	connector.On("SendRequest", mock.Anything, chainIdRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`result`), protocol.JsonRpc))

	validator := validations.NewChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorWrongChainSettingsThenFatalErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &config.UpstreamOptions{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)

	connector.On("SendRequest", mock.Anything, chainIdRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc))
	connector.On("SendRequest", mock.Anything, netVersionRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc))

	validator := validations.NewChainValidator("id", connector, chains.GetChain("ethereum"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.FatalSettingError, actualResult)
}

func TestChainValidatorValidResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &config.UpstreamOptions{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)

	connector.On("SendRequest", mock.Anything, chainIdRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc))
	connector.On("SendRequest", mock.Anything, netVersionRequest).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc))

	validator := validations.NewChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.Valid, actualResult)
}
