package evm_bounds_test

import (
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func evmChain() chains.Chain {
	return chains.GetChain("ethereum").Chain
}

func evmOK(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc)
}

func matchEvmRequest(method string, contains ...string) func(protocol.RequestHolder) bool {
	return func(request protocol.RequestHolder) bool {
		body, err := request.Body()
		if err != nil {
			return false
		}
		bodyStr := string(body)
		if request.Method() != method || request.Id() != "1" || request.RequestType() != protocol.JsonRpc {
			return false
		}
		if !strings.Contains(bodyStr, `"method":"`+method+`"`) {
			return false
		}
		for _, item := range contains {
			if !strings.Contains(bodyStr, item) {
				return false
			}
		}
		return true
	}
}

func expectLatest(connector *mocks.ConnectorMock, latest string) {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_blockNumber"))).
		Return(evmOK(`"` + latest + `"`)).
		Once()
}

func TestEvmLowerBoundDetectorSupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detectors := []struct {
		detector *evm_bounds.EvmLowerBoundDetector
		bt       protocol.LowerBoundType
	}{
		{evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector), protocol.StateBound},
		{evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector), protocol.BlockBound},
		{evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector), protocol.TxBound},
		{evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector), protocol.ReceiptsBound},
	}

	for _, tc := range detectors {
		assert.Equal(t, []protocol.LowerBoundType{tc.bt}, tc.detector.SupportedTypes())
		assert.Equal(t, 3*time.Minute, tc.detector.Period())
	}
}

func TestEvmBlockLowerBoundDetectorBinarySearchesEarliestAvailableBlock(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x5")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`null`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x4"`))).
		Return(evmOK(`{"number":"0x4","transactions":[]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x3"`))).
		Return(evmOK(`{"number":"0x3","transactions":[]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x2"`))).
		Return(evmOK(`null`)).
		Once()

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.BlockBound, result[0].Type)
	assert.Equal(t, int64(3), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmStateLowerBoundDetectorFallsBackToBalanceWhenStateOverrideUnsupported(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call", `"latest"`))).
		Return(evmOK(`"0x"`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBalance", `"0x1"`))).
		Return(evmOK(`"0x0"`)).
		Once()

	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.StateBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmTxLowerBoundDetectorChecksFirstTransactionHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":["0xabc"]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0xabc"`))).
		Return(evmOK(`{"hash":"0xabc"}`)).
		Once()

	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmReceiptsLowerBoundDetectorChecksFirstTransactionObjectHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":[{"hash":"0xdef"}]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionReceipt", `"0xdef"`))).
		Return(evmOK(`{"transactionHash":"0xdef"}`)).
		Once()

	detector := evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.ReceiptsBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmBlockLowerBoundDetectorBinarySearchesMultipleSteps(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x8")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`null`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x5"`))).
		Return(evmOK(`{"number":"0x5","transactions":[]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x3"`))).
		Return(evmOK(`null`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x4"`))).
		Return(evmOK(`{"number":"0x4","transactions":[]}`)).
		Once()

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.BlockBound, result[0].Type)
	assert.Equal(t, int64(4), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmBlockLowerBoundDetectorReusesCachedBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":[]}`)).
		Once()
	expectLatest(connector, "0x4")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":[]}`)).
		Once()

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	first, err := detector.DetectLowerBound()
	require.NoError(t, err)
	second, err := detector.DetectLowerBound()
	require.NoError(t, err)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, int64(1), first[0].Bound)
	assert.Equal(t, int64(1), second[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmStateLowerBoundDetectorParsesStateOverrideResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call", `"latest"`))).
		Return(evmOK(`"0x0000000000000000000000000000000000000000000002fea3085e96a90bf691"`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call", `"0x1"`))).
		Return(evmOK(`"0x0000000000000000000000000000000000000000000002fea3085e96a90bf691"`)).
		Once()

	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.StateBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmTxLowerBoundDetectorParsesLiveBlockAndTransactionShapes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x100000")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(liveEvmBlockWithHashTransactions)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"`))).
		Return(evmOK(liveEvmTransaction)).
		Once()

	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmReceiptsLowerBoundDetectorParsesObjectTransactionsAndReceiptShape(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":[{"hash":"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"}]}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionReceipt", `"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"`))).
		Return(evmOK(liveEvmReceipt)).
		Once()

	detector := evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.ReceiptsBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmLowerBoundDetectorReturnsUnexpectedErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).
		Times(3)

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	assert.Error(t, err)
	assert.Nil(t, result)
	connector.AssertExpectations(t)
}

func TestEvmLowerBoundDetectorRetriesUnexpectedErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("temporary"))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber", `"0x1"`))).
		Return(evmOK(`{"number":"0x1","transactions":[]}`)).
		Once()

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound()

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

const liveEvmBlockWithHashTransactions = `{
  "difficulty":"0xc6d2fa46fd6",
  "gasLimit":"0x2fefd8",
  "gasUsed":"0xa410",
  "hash":"0x9a834c53bbee9c2665a5a84789a1d1ad73750b2d77b50de44f457f411d02e52e",
  "number":"0x100000",
  "parentHash":"0x2dcaf9a8a3fd329d925eb47c1f22022d4f2d562500e546d878bad4247d013858",
  "transactions":[
    "0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa",
    "0xb4216e88df6ebfe666bbe43370d1ddfe8d9c1975d2b8375a522700f7f59c3ed0"
  ]
}`

const liveEvmTransaction = `{
  "type":"0x0",
  "nonce":"0x2f31",
  "gasPrice":"0xba43b7400",
  "gas":"0x15f90",
  "to":"0xdae787ec66e65c60ad35203800b97b45fcb0f909",
  "value":"0x2b81097c919a9e000",
  "hash":"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa",
  "blockHash":"0x9a834c53bbee9c2665a5a84789a1d1ad73750b2d77b50de44f457f411d02e52e",
  "blockNumber":"0x100000",
  "transactionIndex":"0x0",
  "from":"0x68795c4aa09d6f4ed3e5deddf8c2ad3049a601da"
}`

const liveEvmReceipt = `{
  "type":"0x0",
  "status":"0x1",
  "cumulativeGasUsed":"0x5208",
  "logs":[],
  "transactionHash":"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa",
  "transactionIndex":"0x0",
  "blockHash":"0x9a834c53bbee9c2665a5a84789a1d1ad73750b2d77b50de44f457f411d02e52e",
  "blockNumber":"0x100000",
  "gasUsed":"0x5208",
  "effectiveGasPrice":"0xba43b7400",
  "from":"0x68795c4aa09d6f4ed3e5deddf8c2ad3049a601da",
  "to":"0xdae787ec66e65c60ad35203800b97b45fcb0f909",
  "contractAddress":null
}`
