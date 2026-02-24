package validations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestSettingsValidationProcessorMultipleValidators(t *testing.T) {
	conn1, validResultValidator := getTestChainValidator(
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)
	conn2, settingsErrorResultValidator := getTestChainValidator(
		protocol.NewTotalFailureFromErr("1", errors.New("err"), protocol.JsonRpc),
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)
	conn3, fatalErrorResultValidator := getTestChainValidator(
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"1"`), protocol.JsonRpc),
	)

	processor := validations.NewSettingsValidationProcessor(
		[]validations.SettingsValidator{validResultValidator, settingsErrorResultValidator, fatalErrorResultValidator},
	)
	actual := processor.ValidateUpstreamSettings()

	conn1.AssertExpectations(t)
	conn2.AssertExpectations(t)
	conn3.AssertExpectations(t)
	assert.Equal(t, validations.FatalSettingError, actual)
}

func getTestChainValidator(chainIdResp, netVersionResp protocol.ResponseHolder) (*mocks.ConnectorMock, validations.SettingsValidator) {
	connector := mocks.NewConnectorMock()
	options := &config.UpstreamOptions{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil)

	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(chainIdRequest))).
		Return(chainIdResp)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(netVersionRequest))).
		Return(netVersionResp)

	return connector, validations.NewChainValidator("id", connector, chains.GetChain("bsc"), options)
}
