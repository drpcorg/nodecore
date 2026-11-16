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
	// the kinds below, up to blockAuroraEmpty, are empty blocks as live nodes answer them
	blockKlaytnEmpty
	blockEtherlinkEmpty
	blockKavaEmpty
	blockAuroraEmpty
	// blockBareLookingNoGas has no transactions, a root that commits to some and no gasUsed
	blockBareLookingNoGas
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

// Empty blocks as live nodes answer them. Klaytn block 1 carries the keccak of the empty
// string for a root. Etherlink block 2422676 carries the root of block 2422675, which has a
// transaction. Kava block 979963 has no EVM transactions and reports the gas of its cosmos
// ones. Aurora block 9366914 has the zero root.
const (
	liveKlaytnEmpty    = `{"number":"0x1","transactionsRoot":"0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470","gasUsed":"0x0","transactions":[]}`
	liveEtherlinkEmpty = `{"number":"0x24f794","transactionsRoot":"0xf769eb784ffd5cb1edcfeba62ca3f04c9d77b3b1cd489edeb62711486d88056b","gasUsed":"0x0","transactions":[]}`
	liveKavaEmpty      = `{"number":"0xef3fb","transactionsRoot":"` + emptyTxRoot + `","gasUsed":"0x7317d","transactions":[]}`
	liveAuroraEmpty    = `{"number":"0x8eed82","transactionsRoot":"` + zeroTxRoot + `","gasUsed":"0x0","transactions":[]}`
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
	blockKlaytnEmpty:        evmOK(liveKlaytnEmpty),
	blockEtherlinkEmpty:     evmOK(liveEtherlinkEmpty),
	blockKavaEmpty:          evmOK(liveKavaEmpty),
	blockAuroraEmpty:        evmOK(liveAuroraEmpty),
	blockBareLookingNoGas:   evmOK(`{"number":"0x1","transactionsRoot":"0x6199cc9681e6c1793ed9b28d8214ea42642b5dfd89a9b80b1cc20046714dfe0e","transactions":[]}`),
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

// emptyBeforeGold is kindOf on a chain whose first transaction is the gold one, as on
// Ethereum: the blocks before it are empty, so the blocks at the plain bound 1 prove nothing
// and the gold block alone decides.
func emptyBeforeGold(kindOf func(int64) blockKind) func(int64) blockKind {
	return func(h int64) blockKind {
		if h < goldBlock {
			return blockEmpty
		}
		return kindOf(h)
	}
}

// nullBelow is kindOf on a node without the blocks below from, as one restored from a
// snapshot: it answers null there.
func nullBelow(from int64, kindOf func(int64) blockKind) func(int64) blockKind {
	return func(h int64) blockKind {
		if h < from {
			return blockAbsent
		}
		return kindOf(h)
	}
}

const (
	goldBlock = int64(46_147)

	headBlock = int64(1_000_000)
	// bodiesFrom is odd, as goldBlock is: in the test with empty even blocks both hold transactions
	bodiesFrom = headBlock - 14_351
	// headersFrom is the first block of a node restored from a snapshot, above goldBlock
	headersFrom = int64(400_001)
	// askedPastBound mirrors evmLowerBoundMaxOffset
	askedPastBound = int64(20)
)

// goldChain has its gold transaction in goldBlock.
func goldChain() *chains.ConfiguredChain {
	return evmChainWithGold(&chains.GoldLowerBound{Block: uint64(goldBlock), Hash: goldTxHash}, nil)
}

func blockDetector(connector *mocks.ConnectorMock) *evm_bounds.EvmLowerBoundDetector {
	return blockDetectorOn(goldChain(), connector)
}

func blockDetectorOn(chain *chains.ConfiguredChain, connector *mocks.ConnectorMock) *evm_bounds.EvmLowerBoundDetector {
	return fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", chain, time.Second, connector))
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
// would stop at the bare-looking blocks. The chain starts with empty blocks, so the blocks
// at the plain bound prove nothing either.
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
			detector := blockDetector(blocksByKind(headBlock, emptyBeforeGold(bareLookingExceptGold(tc.goldKind))))

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
// does not send it back to the plain search, which trusts headers and answers 1. The chain
// starts with empty blocks: the blocks at the plain bound would not prove it again.
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
			detector := blockDetector(blocksByKind(headBlock, emptyBeforeGold(func(h int64) blockKind {
				if h == goldBlock {
					return goldKind
				}
				return bareHeadersBelow(bodiesFrom)(h)
			})))
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

// A node restored from a snapshot has no early blocks: the gold block answers null and the
// plain search stops at the first header. The bare headers from there on prove the upstream,
// and the block bound is the first block that still has its body.
func TestEvmBlockLowerBoundDetectorFindsFirstBlockWithBodyOnNodeRestoredFromSnapshot(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// On a chain without a gold transaction the blocks at the plain bound are the only proof.
func TestEvmBlockLowerBoundDetectorProvesBareHeadersOnChainWithoutGoldTx(t *testing.T) {
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, bareHeadersBelow(bodiesFrom)))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// The proof asks the blocks up to evmLowerBoundMaxOffset past the plain bound and passes the
// empty ones. A bare header further away is not reached, and the plain bound stays.
func TestEvmBlockLowerBoundDetectorAsksBlocksUpToMaxOffsetPastPlainBound(t *testing.T) {
	tests := []struct {
		name        string
		emptyBlocks int64
		bound       int64
	}{
		{"bare header in the last block asked", askedPastBound, bodiesFrom},
		{"bare header one block further", askedPastBound + 1, headersFrom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetector(blocksByKind(headBlock, nullBelow(headersFrom, func(h int64) blockKind {
				if h < headersFrom+tc.emptyBlocks {
					return blockEmpty
				}
				return bareHeadersBelow(bodiesFrom)(h)
			})))

			assert.Equal(t, tc.bound, detectBlockBound(t, detector))
		})
	}
}

// A block at the plain bound that gives no answer decides nothing, and the next one is asked:
// the upstream is proven in the same cycle. The plain bound is 1 here, as block 0 answers
// with its header.
func TestEvmBlockLowerBoundDetectorAsksNextBlockAfterOneWithoutAnswer(t *testing.T) {
	tests := []struct {
		name string
		kind blockKind
	}{
		{"request fails", blockError},
		{"null", blockAbsent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind {
				if h == 1 {
					return tc.kind
				}
				return bareHeadersBelow(bodiesFrom)(h)
			}))

			assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
		})
	}
}

// An empty block is not a bare header, whatever its root and its gas say. A pruned node
// answers null below headersFrom here and its first blocks are empty: the bound stays where
// the plain search put it. Taken for bare headers, they would move it to the first block
// with transactions.
func TestEvmBlockLowerBoundDetectorTakesNoEmptyBlockForBareHeader(t *testing.T) {
	const emptyBlocks = 5
	tests := []struct {
		name      string
		emptyKind blockKind
	}{
		{"klaytn: keccak of the empty string for a root", blockKlaytnEmpty},
		{"etherlink: root of the previous block", blockEtherlinkEmpty},
		{"kava: gas of the cosmos transactions", blockKavaEmpty},
		{"aurora: zero root", blockAuroraEmpty},
		{"committing root without gasUsed", blockBareLookingNoGas},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, nullBelow(headersFrom, func(h int64) blockKind {
				if h < headersFrom+emptyBlocks {
					return tc.emptyKind
				}
				return blockWithTxs
			})))

			assert.Equal(t, headersFrom, detectBlockBound(t, detector))
		})
	}
}

// An archive node keeps bound 1 on a chain that starts with empty blocks of an unusual root:
// klaytn has no gold transaction, and its first blocks would lift the bound for good.
func TestEvmBlockLowerBoundDetectorKeepsArchiveBoundOnChainStartingWithEmptyBlocks(t *testing.T) {
	const firstBlockWithTxs = 1_000
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind {
		if h < firstBlockWithTxs {
			return blockKlaytnEmpty
		}
		return blockWithTxs
	}))

	assert.Equal(t, int64(1), detectBlockBound(t, detector))
}

// A block with transactions at the plain bound ends the proof: bodies start there, and the
// bare-looking blocks above it are not taken for pruned ones.
func TestEvmBlockLowerBoundDetectorEndsProofAtBlockWithTransactions(t *testing.T) {
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, nullBelow(headersFrom, func(h int64) blockKind {
		if h == headersFrom {
			return blockWithTxs
		}
		return blockBareHeader
	})))

	assert.Equal(t, headersFrom, detectBlockBound(t, detector))
}

// Once the blocks at the plain bound have proven the upstream, a cycle in which they prove
// nothing keeps the verdict. The node drops its oldest headers here and the first ones left
// are of empty blocks: the plain search would answer the first of them.
func TestEvmBlockLowerBoundDetectorKeepsVerdictOfBlocksAtPlainBound(t *testing.T) {
	const laterHeadersFrom = headersFrom + 50_000
	kindOf := nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))
	detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	kindOf = nullBelow(laterHeadersFrom, func(h int64) blockKind {
		if h <= laterHeadersFrom+askedPastBound {
			return blockEmpty
		}
		return bareHeadersBelow(bodiesFrom)(h)
	})

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
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
// the gold block gives the cycle no verdict. The chain starts with empty blocks, so the
// blocks at the plain bound give none either and the plain search answers 1.
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
			detector := blockDetector(serveBlocksByKind(connector, headBlock, emptyBeforeGold(bareHeadersBelow(bodiesFrom))))

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

// The tx probe reads only the transactions of a block: a transactionsRoot or a gasUsed that
// is not a string does not fail it. On a chain this tall a detector whose probes all fail
// reports an error.
func TestEvmTxLowerBoundDetectorIgnoresShapeOfFieldsItDoesNotRead(t *testing.T) {
	tests := []struct {
		name  string
		block string
	}{
		{"transactionsRoot is a number", `{"number":"0x1","transactionsRoot":0,"transactions":["0xabc"]}`},
		{"gasUsed is an object", `{"number":"0x1","gasUsed":{},"transactions":["0xabc"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectLatest(connector, "0x64")
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_getBlockByNumber"))).
				Return(evmOK(tc.block)).
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
		})
	}
}
