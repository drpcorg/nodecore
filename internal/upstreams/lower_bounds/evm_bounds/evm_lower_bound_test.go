package evm_bounds_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// evmChain returns a minimal configured chain with no gold lower bounds, so the
// binary-search path is exercised. Gold short-circuit tests use evmChainWithGold.
func evmChain() *chains.ConfiguredChain {
	return &chains.ConfiguredChain{Chain: chains.GetChain("ethereum").Chain}
}

func evmChainWithGold(tx, receipts *chains.GoldLowerBound) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{
		Chain:       chains.GetChain("ethereum").Chain,
		LowerBounds: &chains.ChainLowerBounds{Tx: tx, Receipts: receipts},
	}
}

func evmOK(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc)
}

// fastEvm shrinks retry backoff so error-path tests stay quick.
func fastEvm(d *evm_bounds.EvmLowerBoundDetector) *evm_bounds.EvmLowerBoundDetector {
	d.SetSearchRetryPolicy(3, time.Millisecond, time.Millisecond)
	return d
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

// evmBlockHeight extracts the numeric block height from an eth_getBlockByNumber request.
func evmBlockHeight(request protocol.RequestHolder) (int64, bool) {
	if request.Method() != "eth_getBlockByNumber" {
		return 0, false
	}
	body, err := request.Body()
	if err != nil {
		return 0, false
	}
	node, err := sonic.Get(body, "params", 0)
	if err != nil {
		return 0, false
	}
	raw, err := node.String()
	if err != nil {
		return 0, false
	}
	h, err := strconv.ParseInt(strings.TrimPrefix(raw, "0x"), 16, 64)
	if err != nil {
		return 0, false
	}
	return h, true
}

func matchBlockAtLeast(threshold int64) func(protocol.RequestHolder) bool {
	return func(r protocol.RequestHolder) bool {
		h, ok := evmBlockHeight(r)
		return ok && h >= threshold
	}
}

func matchBlockBelow(threshold int64) func(protocol.RequestHolder) bool {
	return func(r protocol.RequestHolder) bool {
		h, ok := evmBlockHeight(r)
		return ok && h < threshold
	}
}

func expectLatest(connector *mocks.ConnectorMock, latest string) {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_blockNumber"))).
		Return(evmOK(`"` + latest + `"`)).
		Once()
}

// expectBlocksAbove wires eth_getBlockByNumber to return a block for heights >= threshold and null
// below it, for any number of probes.
func expectBlocksAbove(connector *mocks.ConnectorMock, threshold int64, block string) {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockAtLeast(threshold))).
		Return(evmOK(block)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockBelow(threshold))).
		Return(evmOK(`null`)).
		Maybe()
}

func TestEvmLowerBoundDetectorSupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detectors := []struct {
		detector       *evm_bounds.EvmLowerBoundDetector
		supportedTypes []protocol.LowerBoundType
	}{
		{evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector), []protocol.LowerBoundType{protocol.StateBound, protocol.TraceBound}},
		{evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector), []protocol.LowerBoundType{protocol.BlockBound, protocol.LogsBound}},
		{evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector), []protocol.LowerBoundType{protocol.TxBound}},
		{evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector), []protocol.LowerBoundType{protocol.ReceiptsBound}},
	}

	for _, tc := range detectors {
		assert.Equal(t, tc.supportedTypes, tc.detector.SupportedTypes())
		assert.Equal(t, 3*time.Minute, tc.detector.Period())
	}
}

func TestEvmBlockLowerBoundDetectorBinarySearchesEarliestAvailableBlock(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x5")
	expectBlocksAbove(connector, 3, `{"number":"0x3","transactions":[]}`)

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.BlockBound, result[0].Type)
	assert.Equal(t, int64(3), result[0].Bound)
}

func TestEvmBlockLowerBoundDetectorBinarySearchesMultipleSteps(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x8")
	expectBlocksAbove(connector, 4, `{"number":"0x4","transactions":[]}`)

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.BlockBound, result[0].Type)
	assert.Equal(t, int64(4), result[0].Bound)
}

// The reported bug: genesis (block 1) is retained but there is a hole above it. The detector must
// converge on the real upper boundary (block 6), not trust block 1.
func TestEvmBlockLowerBoundDetectorIgnoresGenesisWhenHoleFollows(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0xa")
	// data at genesis (block 1) and from block 6 onward; hole in 2..5
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBlockHeight(r)
			return ok && (h == 1 || h >= 6)
		})).
		Return(evmOK(`{"number":"0x6","transactions":[]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBlockHeight(r)
			return ok && h >= 2 && h <= 5
		})).
		Return(evmOK(`null`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBlockHeight(r)
			return ok && h == 0
		})).
		Return(evmOK(`null`)).
		Maybe()

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, int64(6), result[0].Bound)
}

func TestEvmStateLowerBoundDetectorFallsBackToBalanceWhenStateOverrideUnsupported(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	// state override probe reports unsupported ("0x"), so detection falls back to eth_getBalance.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call"))).
		Return(evmOK(`"0x"`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBalance"))).
		Return(evmOK(`"0x123"`)).
		Maybe()

	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.StateBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

func TestEvmStateLowerBoundDetectorParsesStateOverrideResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_call"))).
		Return(evmOK(`"0x0000000000000000000000000000000000000000000002fea3085e96a90bf691"`)).
		Maybe()

	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.StateBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

func TestEvmTxLowerBoundDetectorChecksFirstTransactionHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":["0xabc"]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0xabc"`))).
		Return(evmOK(`{"hash":"0xabc"}`)).
		Maybe()

	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

func TestEvmReceiptsLowerBoundDetectorChecksFirstTransactionObjectHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":[{"hash":"0xdef"}]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionReceipt", `"0xdef"`))).
		Return(evmOK(`{"transactionHash":"0xdef"}`)).
		Maybe()

	detector := evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.ReceiptsBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

// Block detection (plain variant) stays stable across repeated runs: the second detection reuses
// the cached bound as the left edge of its range and still reports 1.
func TestEvmBlockLowerBoundDetectorStableAcrossRuns(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	expectLatest(connector, "0x4")
	expectBlocksAbove(connector, 0, `{"number":"0x1","transactions":[]}`)

	detector := evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)

	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
	assert.Equal(t, int64(1), first[0].Bound)
	assert.Equal(t, int64(1), second[0].Bound)
}

// The offset variant (Tx detector) confirms a previously found bound with a single probe.
func TestEvmTxLowerBoundDetectorReusesCachedBoundWithSingleProbe(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":["0xabc"]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash"))).
		Return(evmOK(`{"hash":"0xabc"}`)).
		Maybe()

	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), first[0].Bound)

	expectLatest(connector, "0x4")
	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), second[0].Bound)
}

func TestEvmTxLowerBoundDetectorParsesLiveBlockAndTransactionShapes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x100000")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(liveEvmBlockWithHashTransactions)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"`))).
		Return(evmOK(liveEvmTransaction)).
		Maybe()

	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

func TestEvmReceiptsLowerBoundDetectorParsesObjectTransactionsAndReceiptShape(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":[{"hash":"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"}]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionReceipt", `"0x01c5a8461d06c2c195035c148af0f871c7679841d86ae5bb98676bb2d8e68dfa"`))).
		Return(evmOK(liveEvmReceipt)).
		Maybe()

	detector := evm_bounds.NewEvmReceiptsLowerBoundDetector("id", evmChain(), time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.ReceiptsBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
}

// A persistent (unclassified) error is now treated as "no data, search higher"; on a tall chain
// where nothing is ever confirmed, the convergence guard fails detection so the cached bound is kept.
func TestEvmLowerBoundDetectorFailsWhenEverythingErrorsOnTallChain(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x64") // 100
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).
		Maybe()

	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector))

	result, err := detector.DetectLowerBound(context.Background())

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestEvmLowerBoundDetectorRetriesTransientErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x3")
	// first block probe errors once, then every probe succeeds -> detection completes.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("temporary"))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":[]}`)).
		Maybe()

	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", evmChain(), time.Second, connector))

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, int64(1), result[0].Bound)
}

const goldTxHash = "0x5c504ed432cb51138bcf09aa5e8a410dd4a1e204ef84bfed1be16dfba1b22060"

// When the chain has a gold tx hash and the upstream serves that ancient tx, the
// detector short-circuits to bound 1 with a single probe and never fetches blocks.
func TestEvmTxLowerBoundDetectorShortCircuitsOnGoldBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"`+goldTxHash+`"`))).
		Return(evmOK(`{"hash":"` + goldTxHash + `"}`)).
		Once()

	chain := evmChainWithGold(&chains.GoldLowerBound{Block: 46147, Hash: goldTxHash}, nil)
	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", chain, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

func TestEvmReceiptsLowerBoundDetectorShortCircuitsOnGoldBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionReceipt", `"`+goldTxHash+`"`))).
		Return(evmOK(`{"transactionHash":"` + goldTxHash + `"}`)).
		Once()

	chain := evmChainWithGold(nil, &chains.GoldLowerBound{Block: 46147, Hash: goldTxHash})
	detector := evm_bounds.NewEvmReceiptsLowerBoundDetector("id", chain, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.ReceiptsBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	connector.AssertExpectations(t)
}

// When the gold probe returns null (upstream pruned that tx), the detector falls
// back to the normal binary search.
func TestEvmTxLowerBoundDetectorFallsBackWhenGoldBoundMissing(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"`+goldTxHash+`"`))).
		Return(evmOK(`null`)).
		Once()
	expectLatest(connector, "0x3")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactions":["0xabc"]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0xabc"`))).
		Return(evmOK(`{"hash":"0xabc"}`)).
		Maybe()

	chain := evmChainWithGold(&chains.GoldLowerBound{Block: 46147, Hash: goldTxHash}, nil)
	detector := evm_bounds.NewEvmTxLowerBoundDetector("id", chain, time.Second, connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, result)
	assert.Equal(t, protocol.TxBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
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
