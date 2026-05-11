package validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestChainValidatorChaiIdErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	expectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewTotalFailure(chainIdRequest, protocol.RequestTimeoutError()),
	)
	expectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"1"`), protocol.JsonRpc),
	)

	validator := validations.NewEthChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorNetVersionErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	expectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewTotalFailure(netVersionRequest, protocol.RequestTimeoutError()),
	)
	expectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	)

	validator := validations.NewEthChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorWrongChainSettingsThenFatalErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	expectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	expectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)

	validator := validations.NewEthChainValidator("id", connector, chains.GetChain("ethereum"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.FatalSettingError, actualResult)
}

func TestChainValidatorValidResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	expectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	expectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)

	validator := validations.NewEthChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.Valid, actualResult)
}

func expectEthValidationRequest(
	connector *mocks.ConnectorMock,
	request protocol.RequestHolder,
	response protocol.ResponseHolder,
) {
	call := connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(request))).
		Return(response)

	if response.HasError() {
		call.Times(validations.RetryMaxAttempts)
		return
	}

	call.Once()
}
