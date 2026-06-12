package eth_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthLogIndexValidatorReturnsValidForGlobalIndexes(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"},{"logIndex":"0x1"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x2"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsFatalForPerTransactionReset(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"},{"logIndex":"0x1"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsCachedResultBetweenChecks(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorKeepsLastResultOnReadError(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsFatalForSingleLogTransactionReset(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsValidWhenTransactionsHaveNoLogs(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[]}`),
		jsonRpcResult(`{"logs":[]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorSearchesPreviousBlockWhenInsufficientTransactions(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"}]}`),
		jsonRpcResult(`{"transactions":[{"hash":"0x2"},{"hash":"0x3"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x1"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsFatalForNonContinuousIndexes(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"},{"logIndex":"0x1"},{"logIndex":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x5"},{"logIndex":"0x6"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsFatalForIncorrectFirstLogIndex(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x5"},{"logIndex":"0x6"},{"logIndex":"0x7"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x8"},{"logIndex":"0x9"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorValidatesAgainOnEleventhCall(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"},{"hash":"0x2"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x1"}]}`),
		jsonRpcResult(`"0x65"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x3"},{"hash":"0x4"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x0"}]}`),
		jsonRpcResult(`{"logs":[{"logIndex":"0x1"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	for i := 0; i < 9; i++ {
		assert.Equal(t, validations.Valid, validator.Validate())
	}
	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthLogIndexValidatorReturnsLastResultWhenCannotFindSuitableBlock(t *testing.T) {
	connector := newLogIndexConnectorMock([]protocol.ResponseHolder{
		jsonRpcResult(`"0x64"`),
		jsonRpcResult(`{"transactions":[{"hash":"0x1"}]}`),
		jsonRpcResult(`{"transactions":[{"hash":"0x2"}]}`),
		jsonRpcResult(`{"transactions":[{"hash":"0x3"}]}`),
		jsonRpcResult(`{"transactions":[{"hash":"0x4"}]}`),
		jsonRpcResult(`{"transactions":[{"hash":"0x5"}]}`),
	})
	validator := newLogIndexValidator(connector)

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func newLogIndexValidator(connector *mocks.ConnectorMock) *eth_validations.EthLogIndexValidator {
	return eth_validations.NewEthLogIndexValidator("upstream-1", connector, chains.GetChain("ethereum"), &chains.Options{InternalTimeout: time.Second})
}

func newLogIndexConnectorMock(responses []protocol.ResponseHolder) *mocks.ConnectorMock {
	connector := mocks.NewConnectorMock()
	for _, response := range responses {
		call := connector.On("SendRequest", mock.Anything, mock.Anything).Return(response).Once()
		if response.HasError() {
			call.Times(validations.RetryMaxAttempts)
		}
	}
	return connector
}

func jsonRpcResult(result string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(result), protocol.JsonRpc)
}
