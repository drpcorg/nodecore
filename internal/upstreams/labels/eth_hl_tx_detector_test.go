package labels_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEthHLTxLabelsDetectorDetectLabelsReturnsNilForUnsupportedChain(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := labels.NewEthHLTxLabelsDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

	result := detector.DetectLabels()

	assert.Nil(t, result)
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestEthHLTxLabelsDetectorDetectLabelsReturnsTrueWhenNativeTxIsFound(t *testing.T) {
	latestBlockRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.HYPERLIQUID)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	receiptBlocks := newReceiptBlockRecorder()

	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(latestBlockRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x12c"`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchHLReceiptRequest("0x12a"))).
		Run(func(args mock.Arguments) {
			receiptBlocks.Record(args.Get(1).(protocol.RequestHolder))
		}).
		Return(protocol.NewSimpleHttpUpstreamResponse(
			"1",
			[]byte(`[{"from":"0x2222222222222222222222222222222222222222"}]`),
			protocol.JsonRpc,
		)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAnyHLReceiptRequest)).
		Run(func(args mock.Arguments) {
			receiptBlocks.Record(args.Get(1).(protocol.RequestHolder))
		}).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc))

	detector := labels.NewEthHLTxLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"include_hl_native_tx": "true",
		"exclude_hl_native_tx": "false",
	}, result)
	assert.Contains(t, receiptBlocks.Snapshot(), "0x12a")
	connector.AssertExpectations(t)
}

func TestEthHLTxLabelsDetectorDetectLabelsReturnsFalseWhenNoNativeTxIsFound(t *testing.T) {
	latestBlockRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.HYPERLIQUID_TESTNET)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	receiptBlocks := newReceiptBlockRecorder()

	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(latestBlockRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x12c"`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAnyHLReceiptRequest)).
		Run(func(args mock.Arguments) {
			receiptBlocks.Record(args.Get(1).(protocol.RequestHolder))
		}).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc))

	detector := labels.NewEthHLTxLabelsDetector("upstream-id", chains.HYPERLIQUID_TESTNET, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"include_hl_native_tx": "false",
		"exclude_hl_native_tx": "true",
	}, result)
	assert.Len(t, receiptBlocks.Snapshot(), 300)
	assert.Contains(t, receiptBlocks.Snapshot(), "0x12c")
	assert.Contains(t, receiptBlocks.Snapshot(), "0x1")
	connector.AssertExpectations(t)
}

func TestEthHLTxLabelsDetectorDetectLabelsRunsOnlyEveryFifthProbe(t *testing.T) {
	latestBlockRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.HYPERLIQUID)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	receiptBlocks := newReceiptBlockRecorder()

	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(latestBlockRequest))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x12c"`), protocol.JsonRpc)).
		Twice()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAnyHLReceiptRequest)).
		Run(func(args mock.Arguments) {
			receiptBlocks.Record(args.Get(1).(protocol.RequestHolder))
		}).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc))

	detector := labels.NewEthHLTxLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	first := detector.DetectLabels()
	for i := 0; i < 4; i++ {
		assert.Nil(t, detector.DetectLabels())
	}
	sixth := detector.DetectLabels()

	assert.Equal(t, map[string]string{
		"include_hl_native_tx": "false",
		"exclude_hl_native_tx": "true",
	}, first)
	assert.Equal(t, map[string]string{
		"include_hl_native_tx": "false",
		"exclude_hl_native_tx": "true",
	}, sixth)
	assert.Len(t, receiptBlocks.Snapshot(), 600)
	connector.AssertExpectations(t)
}

func TestEthHLTxLabelsDetectorDetectLabelsReturnsNilWhenLatestBlockLookupFails(t *testing.T) {
	latestBlockRequest, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, chains.HYPERLIQUID)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(latestBlockRequest))).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := labels.NewEthHLTxLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	require.Nil(t, result)
	connector.AssertExpectations(t)
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(matchAnyHLReceiptRequest))
}

type receiptBlockRecorder struct {
	mu     sync.Mutex
	blocks []string
}

func newReceiptBlockRecorder() *receiptBlockRecorder {
	return &receiptBlockRecorder{}
}

func (r *receiptBlockRecorder) Record(request protocol.RequestHolder) {
	blockHex, err := receiptBlockHex(request)
	if err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocks = append(r.blocks, blockHex)
}

func (r *receiptBlockRecorder) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	blocks := make([]string, len(r.blocks))
	copy(blocks, r.blocks)
	return blocks
}

func matchAnyHLReceiptRequest(request protocol.RequestHolder) bool {
	if request == nil || request.Method() != "eth_getBlockReceipts" || request.RequestType() != protocol.JsonRpc {
		return false
	}

	_, err := receiptBlockHex(request)
	return err == nil
}

func matchHLReceiptRequest(expectedBlock string) func(protocol.RequestHolder) bool {
	return func(request protocol.RequestHolder) bool {
		if !matchAnyHLReceiptRequest(request) {
			return false
		}

		blockHex, err := receiptBlockHex(request)
		return err == nil && blockHex == expectedBlock
	}
}

func receiptBlockHex(request protocol.RequestHolder) (string, error) {
	body, err := request.Body()
	if err != nil {
		return "", err
	}

	var payload struct {
		Params []json.RawMessage `json:"params"`
	}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if len(payload.Params) != 1 {
		return "", errors.New("unexpected receipt params")
	}

	var blockHex string
	if err := sonic.Unmarshal(payload.Params[0], &blockHex); err != nil {
		return "", err
	}
	return blockHex, nil
}
