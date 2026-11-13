package evm_bounds_test

import (
	"context"
	"fmt"
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

type blockKind int

const (
	blockAbsent blockKind = iota
	blockWithTxs
	// blockEmpty is a block that really has no transactions
	blockEmpty
	// blockBareHeader has no transactions although its header commits to some
	blockBareHeader
	// the kinds below, up to blockNoRoot, have no transactions and a root that commits to nothing
	blockUpperCaseEmptyRoot
	blockZeroRoot
	blockShortRoot
	blockUnprefixedRoot
	blockNoRoot
	blockError
)

// liveBareHeader is the answer of a reth-bsc node in minimal mode for BSC block 131910656,
// which has 66 transactions.
const liveBareHeader = `{
  "number":"0x7dccc00",
  "hash":"0x0960fb3de7bd99d1d3350f4dc37ed201ae21b6d0bae866e246c0c570201cedd5",
  "transactionsRoot":"0x6199cc9681e6c1793ed9b28d8214ea42642b5dfd89a9b80b1cc20046714dfe0e",
  "gasUsed":"0x8e8a5f",
  "size":"0x385",
  "transactions":[],
  "withdrawals":[]
}`

const (
	emptyTxRoot = "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"
	zeroTxRoot  = "0x0000000000000000000000000000000000000000000000000000000000000000"
)

var blockAnswers = map[blockKind]protocol.ResponseHolder{
	blockAbsent:             evmOK(`null`),
	blockWithTxs:            evmOK(`{"number":"0x1","transactionsRoot":"0x6199cc9681e6c1793ed9b28d8214ea42642b5dfd89a9b80b1cc20046714dfe0e","transactions":["0xaa"]}`),
	blockEmpty:              evmOK(`{"number":"0x1","transactionsRoot":"` + emptyTxRoot + `","transactions":[]}`),
	blockBareHeader:         evmOK(liveBareHeader),
	blockUpperCaseEmptyRoot: evmOK(`{"number":"0x1","transactionsRoot":"0x` + strings.ToUpper(emptyTxRoot[2:]) + `","transactions":[]}`),
	blockZeroRoot:           evmOK(`{"number":"0x1","transactionsRoot":"` + zeroTxRoot + `","transactions":[]}`),
	blockShortRoot:          evmOK(`{"number":"0x1","transactionsRoot":"0x","transactions":[]}`),
	blockUnprefixedRoot:     evmOK(`{"number":"0x1","transactionsRoot":"zz` + strings.Repeat("ab", 32) + `","transactions":[]}`),
	blockNoRoot:             evmOK(`{"number":"0x1","transactions":[]}`),
	blockError:              protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom")),
}

// blocksByKind serves a head and, for every height, the answer kindOf picks.
func blocksByKind(head int64, kindOf func(int64) blockKind) *mocks.ConnectorMock {
	return serveBlocksByKind(mocks.NewConnectorMock(), head, kindOf)
}

// serveBlocksByKind adds the answers of blocksByKind to a connector that already has some.
func serveBlocksByKind(connector *mocks.ConnectorMock, head int64, kindOf func(int64) blockKind) *mocks.ConnectorMock {
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_blockNumber"))).
		Return(evmOK(fmt.Sprintf(`"0x%x"`, head))).
		Maybe()
	for kind, answer := range blockAnswers {
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
				h, ok := evmBlockHeight(r)
				return ok && kindOf(h) == kind
			})).
			Return(answer).
			Maybe()
	}
	return connector
}

// bareHeadersBelow is an upstream that pruned the bodies below bodiesFrom and answers those
// blocks with bare headers.
func bareHeadersBelow(bodiesFrom int64) func(int64) blockKind {
	return func(h int64) blockKind {
		if h >= bodiesFrom {
			return blockWithTxs
		}
		return blockBareHeader
	}
}

// bareLookingExceptGold is an upstream with every body in place, on a chain whose
// transactionsRoot covers more than the listed transactions: its blocks look like bare headers.
func bareLookingExceptGold(goldKind blockKind) func(int64) blockKind {
	return func(h int64) blockKind {
		if h == goldBlock {
			return goldKind
		}
		return blockBareHeader
	}
}

const (
	goldBlock = int64(46_147)

	headBlock = int64(1_000_000)
	// bodiesFrom is odd, as goldBlock is: in the test with empty even blocks both hold transactions
	bodiesFrom = headBlock - 14_351
)

// goldChain has its gold transaction in goldBlock.
func goldChain() *chains.ConfiguredChain {
	return evmChainWithGold(&chains.GoldLowerBound{Block: uint64(goldBlock), Hash: goldTxHash}, nil)
}

func blockDetector(connector *mocks.ConnectorMock) *evm_bounds.EvmLowerBoundDetector {
	return fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))
}

func detectBlockBound(t *testing.T, detector *evm_bounds.EvmLowerBoundDetector) int64 {
	t.Helper()
	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, protocol.BlockBound, result[0].Type)
	return result[0].Bound
}

// reth with pruned bodies answers every old block with its bare header. The block bound is the
// first block that still has its body, not 1.
func TestEvmBlockLowerBoundDetectorFindsFirstBlockWithBodyAboveBareHeaders(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, bareHeadersBelow(bodiesFrom)))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// The gold block has its transactions, so blocks that look like bare headers are not taken
// for pruned ones and the upstream keeps bound 1.
func TestEvmBlockLowerBoundDetectorTrustsHeadersWhenGoldBlockHasItsBody(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, bareLookingExceptGold(blockWithTxs)))

	assert.Equal(t, int64(1), detectBlockBound(t, detector))
}

// A gold block without transactions proves a pruned body only when its root commits to some.
// Otherwise the upstream stays on the plain search, which here answers 1; the body search
// would stop at the bare-looking blocks.
func TestEvmBlockLowerBoundDetectorNeedsCommittingRootToProveBareHeaders(t *testing.T) {
	tests := []struct {
		name     string
		goldKind blockKind
	}{
		{"empty trie root", blockEmpty},
		{"empty trie root in upper case", blockUpperCaseEmptyRoot},
		{"zero root", blockZeroRoot},
		{"root shorter than a hash", blockShortRoot},
		{"root of a hash length without 0x", blockUnprefixedRoot},
		{"no root", blockNoRoot},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetector(blocksByKind(headBlock, bareLookingExceptGold(tc.goldKind)))

			assert.Equal(t, int64(1), detectBlockBound(t, detector))
		})
	}
}

// A block that really has no transactions looks the same with and without its body, so it
// decides nothing: the nearest block with transactions does. Every even block is empty here,
// on both sides of bodiesFrom.
func TestEvmBlockLowerBoundDetectorSkipsEmptyBlocksInBodySearch(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind {
		if h%2 == 0 {
			return blockEmpty
		}
		return bareHeadersBelow(bodiesFrom)(h)
	}))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// Once the upstream is known to serve bare headers, a gold block answer that decides nothing
// does not send it back to the plain search, which trusts headers and answers 1.
func TestEvmBlockLowerBoundDetectorKeepsBareHeadersVerdictWithoutGoldBlockAnswer(t *testing.T) {
	tests := []struct {
		name     string
		goldKind blockKind
	}{
		{"request fails", blockError},
		{"null", blockAbsent},
		{"block without transactions and with the empty trie root", blockEmpty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			goldKind := blockBareHeader
			detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind {
				if h == goldBlock {
					return goldKind
				}
				return bareHeadersBelow(bodiesFrom)(h)
			}))
			require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

			goldKind = tc.goldKind

			assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
		})
	}
}

// The verdict goes when the gold block shows its transactions: the plain search answers 1
// again, where the body search would stop at the bare-looking blocks.
func TestEvmBlockLowerBoundDetectorDropsBareHeadersVerdictWhenGoldBlockShowsItsBody(t *testing.T) {
	kindOf := bareHeadersBelow(bodiesFrom)
	detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	kindOf = bareLookingExceptGold(blockWithTxs)

	assert.Equal(t, int64(1), detectBlockBound(t, detector))
}

func isGoldBlockRequest(r protocol.RequestHolder) bool {
	h, ok := evmBlockHeight(r)
	return ok && h == goldBlock
}

func goldBlockRequests(connector *mocks.ConnectorMock) int {
	count := 0
	for _, call := range connector.Calls {
		if request, ok := call.Arguments.Get(1).(protocol.RequestHolder); ok && isGoldBlockRequest(request) {
			count++
		}
	}
	return count
}

// A failed gold block request is repeated, three attempts in all, and the first answer ends
// them. An upstream proven on a repeat gets its bound in the same cycle; after three failures
// the cycle has no verdict and the plain search answers 1.
func TestEvmBlockLowerBoundDetectorRepeatsFailedGoldBlockRequest(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		requests int
		bound    int64
	}{
		{"fails once", 1, 2, bodiesFrom},
		{"fails twice", 2, 3, bodiesFrom},
		{"fails three times", 3, 3, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(isGoldBlockRequest)).
				Return(blockAnswers[blockError]).
				Times(tc.failures)
			detector := blockDetector(serveBlocksByKind(connector, headBlock, bareHeadersBelow(bodiesFrom)))

			assert.Equal(t, tc.bound, detectBlockBound(t, detector))
			assert.Equal(t, tc.requests, goldBlockRequests(connector))
		})
	}
}

// The pause between attempts ends when the context does: the detector does not hold a
// stopping upstream for the rest of the pause.
func TestEvmBlockLowerBoundDetectorLeavesGoldBlockPauseWhenContextEnds(t *testing.T) {
	const pause = 2 * time.Second
	detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind {
		if h == goldBlock {
			return blockError
		}
		return bareHeadersBelow(bodiesFrom)(h)
	}))
	detector.SetGoldBlockRetryDelay(pause)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, _ = detector.DetectLowerBound(ctx)

	assert.Less(t, time.Since(started), pause)
}

// The tx probe reads only the transactions of a block: a transactionsRoot that is not a
// string does not fail it. On a chain this tall a detector whose probes all fail reports an
// error.
func TestEvmTxLowerBoundDetectorIgnoresTransactionsRootShape(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, "0x64")
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
		Return(evmOK(`{"number":"0x1","transactionsRoot":0,"transactions":["0xabc"]}`)).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getTransactionByHash", `"0xabc"`))).
		Return(evmOK(`{"hash":"0xabc"}`)).
		Maybe()

	detector := fastEvm(evm_bounds.NewEvmTxLowerBoundDetector("id", evmChain(), time.Second, connector))

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].Bound)
}
