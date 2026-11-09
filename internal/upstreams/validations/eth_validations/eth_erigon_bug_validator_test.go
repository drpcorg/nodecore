package eth_validations_test

import (
	"context"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const erigonProbeData = `"0x0000000000000000000000000000000000000000000000000de0b6b3a7640000"`

func TestEthErigonBugValidatorReturnsValidForNonErigonClient(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"Geth/v1.14.0"`)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorReturnsFatalWhenProbeReturnsEmptyResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0/linux-amd64/go1.22"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x64"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x32")).Return(jsonRpcResult(`"0x"`)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorSearchesLeftOnDataAndReturnsValid(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"Erigon/v3.0.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x8"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x4")).Return(jsonRpcResult(erigonProbeData)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x2")).Return(jsonRpcResult(erigonProbeData)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x1")).Return(jsonRpcResult(erigonProbeData)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorReturnsSettingsErrorWhenNoProbeAnswersWithData(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x8"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x4")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x6")).Return(jsonRpcResult(`{"not":"a string"}`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x7")).Return(jsonRpcResult(`"garbage"`)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorReturnsValidWhenSearchSawDataBeforeErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x8"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x4")).Return(jsonRpcResult(erigonProbeData)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x2")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x3")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorReturnsSettingsErrorWhenFirstClientVersionFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorFindsBugBehindPrunedState(t *testing.T) {
	// state pruned below block 5, bug at every block with state
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x8"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x4")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x6")).Return(jsonRpcResult(`"0x"`)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorStopsAtMaxDepth(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x10000000000"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_call")).Return(jsonRpcResult(erigonProbeData)).Times(21)
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorCachesSettingsErrorWhenClientVersionFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x64"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x32")).Return(jsonRpcResult(`"0x"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	for i := 0; i < 9; i++ {
		assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	}
	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorReturnsSettingsErrorWhenBlockNumberFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x64"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x32")).Return(jsonRpcResult(`"0x"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	for i := 0; i < 9; i++ {
		assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	}
	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorRecoversOnCleanCheck(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x64"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x32")).Return(jsonRpcResult(`"0x"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.61.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x2"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonProbe("0x1")).Return(jsonRpcResult(erigonProbeData)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	for i := 0; i < 9; i++ {
		assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	}
	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func TestEthErigonBugValidatorSendsStateOverrideProbe(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, erigonRequest("web3_clientVersion")).Return(jsonRpcResult(`"erigon/2.60.0"`)).Once()
	connector.On("SendRequest", mock.Anything, erigonRequest("eth_blockNumber")).Return(jsonRpcResult(`"0x2"`)).Once()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(request protocol.RequestHolder) bool {
		if request.Method() != "eth_call" {
			return false
		}
		body, err := request.Body()
		if err != nil {
			return false
		}
		params, _ := sonic.Get(body, "params")
		to, _ := params.Index(0).Get("to").String()
		data, _ := params.Index(0).Get("data").String()
		code, _ := params.Index(2).Get("0x1111111111111111111111111111111111111111").Get("code").String()
		return to == "0x1111111111111111111111111111111111111111" && data == "0x1eaf190c" && len(code) > 2
	})).Return(jsonRpcResult(erigonProbeData)).Once()
	validator := newErigonBugValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}

func newErigonBugValidator(connector *mocks.ConnectorMock) *eth_validations.EthErigonBugValidator {
	return eth_validations.NewEthErigonBugValidator("upstream-1", connector, chains.GetChain("ethereum"), &chains.Options{InternalTimeout: time.Second})
}

func erigonRequest(method string) any {
	return mock.MatchedBy(func(request protocol.RequestHolder) bool {
		return request.Method() == method
	})
}

func erigonProbe(blockTag string) any {
	return mock.MatchedBy(func(request protocol.RequestHolder) bool {
		if request.Method() != "eth_call" {
			return false
		}
		body, err := request.Body()
		if err != nil {
			return false
		}
		tag, err := sonic.Get(body, "params", 1)
		if err != nil {
			return false
		}
		value, err := tag.String()
		return err == nil && value == blockTag
	})
}
