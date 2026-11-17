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
  "transactionsRoot":"` + committingTxRoot + `",
  "gasUsed":"0x8e8a5f",
  "size":"0x385",
  "transactions":[],
  "withdrawals":[]
}`

const (
	emptyTxRoot = "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"
	zeroTxRoot  = "0x0000000000000000000000000000000000000000000000000000000000000000"
	// committingTxRoot is the transactionsRoot of BSC block 131910656: it commits to transactions
	committingTxRoot = "0x6199cc9681e6c1793ed9b28d8214ea42642b5dfd89a9b80b1cc20046714dfe0e"
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
	blockWithTxs:            evmOK(`{"number":"0x1","transactionsRoot":"` + committingTxRoot + `","transactions":["0xaa"]}`),
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
	blockBareLookingNoGas:   evmOK(`{"number":"0x1","transactionsRoot":"` + committingTxRoot + `","transactions":[]}`),
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
// Ethereum: the blocks before it are empty, so the blocks near the plain bound 1 prove
// nothing and the gold block alone decides.
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
	// askedPastBound is how far past the plain bound the detector asks for a bare header
	askedPastBound = int64(evm_bounds.BareHeaderProofWindow)
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

// The gold block with its transactions settles the cycle: the blocks near the plain bound are
// not asked. Here they look bare with their bodies in place and later blocks list their
// transactions: asked, the first ones would pass for pruned and move the bound to listedFrom.
func TestEvmBlockLowerBoundDetectorAsksNoBlocksNearPlainBoundWhenGoldBlockHasItsBody(t *testing.T) {
	const listedFrom = 1_000
	detector := blockDetector(blocksByKind(headBlock, bareHeadersBelow(listedFrom)))

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
// on both sides of bodiesFrom. That holds for an empty block whose root commits to something,
// as on klaytn and etherlink: taken for a bare header, it would stop the search under the head.
func TestEvmBlockLowerBoundDetectorSkipsEmptyBlocksInBodySearch(t *testing.T) {
	tests := []struct {
		name      string
		emptyKind blockKind
	}{
		{"empty trie root", blockEmpty},
		{"klaytn: keccak of the empty string for a root", blockKlaytnEmpty},
		{"etherlink: root of the previous block", blockEtherlinkEmpty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind {
				if h%2 == 0 {
					return tc.emptyKind
				}
				return bareHeadersBelow(bodiesFrom)(h)
			}))

			assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
		})
	}
}

// Once the upstream is known to serve bare headers, a gold block answer that decides nothing
// does not send it back to the plain search, which trusts headers and answers 1. The chain
// starts with empty blocks: the blocks near the plain bound would not prove it again.
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
// again, where the body search would stop at the bare-looking blocks. It stays gone in the
// next cycle, which the gold block does not answer.
func TestEvmBlockLowerBoundDetectorDropsBareHeadersVerdictWhenGoldBlockShowsItsBody(t *testing.T) {
	kindOf := bareHeadersBelow(bodiesFrom)
	detector := blockDetector(blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	kindOf = bareLookingExceptGold(blockWithTxs)
	assert.Equal(t, int64(1), detectBlockBound(t, detector))

	kindOf = bareLookingExceptGold(blockAbsent)
	assert.Equal(t, int64(1), detectBlockBound(t, detector))
}

// A node restored from a snapshot has no early blocks: the gold block answers null and the
// plain search stops at the first header. The bare headers from there on prove the upstream,
// and the block bound is the first block that still has its body.
func TestEvmBlockLowerBoundDetectorFindsFirstBlockWithBodyOnNodeRestoredFromSnapshot(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// On a chain without a gold transaction the blocks near the plain bound are the only proof.
func TestEvmBlockLowerBoundDetectorProvesBareHeadersOnChainWithoutGoldTx(t *testing.T) {
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, bareHeadersBelow(bodiesFrom)))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// A bare header near the plain bound proves the upstream only if the body search then meets
// a block with its body. Every block looks bare here, on a chain whose transactionsRoot covers
// more than the listed transactions: nothing is pruned, and the bound stays 1 where the body
// search would answer the head. An empty block is no body: in the last case the body search
// passes the empty blocks under the head and stops at the first bare-looking one.
func TestEvmBlockLowerBoundDetectorNeedsBlockWithBodyToProveBareHeaders(t *testing.T) {
	const emptyFrom = headBlock - 2_000
	tests := []struct {
		name   string
		chain  *chains.ConfiguredChain
		kindOf func(int64) blockKind
	}{
		{"gold block request fails", goldChain(), bareLookingExceptGold(blockError)},
		{"chain without a gold transaction", evmChain(), bareLookingExceptGold(blockBareHeader)},
		{"empty blocks under the head", evmChain(), func(h int64) blockKind {
			if h >= emptyFrom {
				return blockEmpty
			}
			return blockBareHeader
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetectorOn(tc.chain, blocksByKind(headBlock, tc.kindOf))

			assert.Equal(t, int64(1), detectBlockBound(t, detector))
		})
	}
}

// A rejected proof is not tried again while the plain bound stays: the next cycle asks no
// block of the upper quarter of the chain. Only the body search goes there on this upstream:
// with bound 1 the plain search asks nothing above the middle of the chain, and the body
// search starts under the head.
func TestEvmBlockLowerBoundDetectorDoesNotRetryRejectedProofWhilePlainBoundStays(t *testing.T) {
	const upperQuarter = headBlock * 3 / 4
	connector := blocksByKind(headBlock, bareLookingExceptGold(blockBareHeader))
	detector := blockDetectorOn(evmChain(), connector)
	detectBlockBound(t, detector)
	askedUnderHead := blockRequestsAbove(connector, upperQuarter)

	detectBlockBound(t, detector)

	assert.Equal(t, askedUnderHead, blockRequestsAbove(connector, upperQuarter))
}

// everyNthLooksBare is an upstream with every body in place, on a chain where one block in
// period lists no transactions although its header commits to some: block 1 and every
// period-th after it.
func everyNthLooksBare(period int64) func(int64) blockKind {
	return func(h int64) blockKind {
		if h%period == 1 {
			return blockBareHeader
		}
		return blockWithTxs
	}
}

// pairsLookBare is everyNthLooksBare with the bare-looking blocks coming in pairs: blocks 0
// and 1 and the two at every period after them.
func pairsLookBare(period int64) func(int64) blockKind {
	return func(h int64) blockKind {
		if h%period <= 1 {
			return blockBareHeader
		}
		return blockWithTxs
	}
}

// lateHeadersFrom is the first block of a node restored from a late snapshot: most of the
// chain is missing on it.
const lateHeadersFrom = int64(900_001)

// Blocks with transactions between a bare header and the bound of the body search show that
// nothing is pruned there: the bare-looking blocks stand among whole ones, and the bound
// stays where the plain search put it. The body search stops at one of them, at 13456 with
// every second block bare-looking and near the head with every third or fifth.
//
// The blocks asked are drawn at random, as blocks spaced by a rule fall in step with
// bare-looking blocks that come by a rule. The last three cases are heads where such spacing
// asked bare-looking blocks only: five blocks spaced evenly, and five spaced one block wider
// each, which a pair of bare-looking blocks in seven or in five keeps up with.
func TestEvmBlockLowerBoundDetectorTakesNoBareLookingBlocksAmongWholeOnesForPruned(t *testing.T) {
	tests := []struct {
		name   string
		head   int64
		kindOf func(int64) blockKind
		bound  int64
	}{
		{"every second block looks bare", headBlock, everyNthLooksBare(2), 1},
		{"every third block looks bare", headBlock, everyNthLooksBare(3), 1},
		{"every fifth block looks bare", headBlock, everyNthLooksBare(5), 1},
		{"pruned node, every third block looks bare", headBlock, nullBelow(lateHeadersFrom, everyNthLooksBare(3)), lateHeadersFrom},
		{"every second block looks bare, evenly spaced blocks all do", headBlock + 2, everyNthLooksBare(2), 1},
		{"two blocks in seven look bare", headBlock, pairsLookBare(7), 1},
		{"two blocks in five look bare", headBlock + 1, pairsLookBare(5), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detector := blockDetectorOn(evmChain(), blocksByKind(tc.head, tc.kindOf))

			assert.Equal(t, tc.bound, detectBlockBound(t, detector))
		})
	}
}

// The bare header of the gold block needs no block with a body to count: that block is known
// to hold a transaction. An upstream that shows no body at all gets the head for a bound.
// This is not a goal but the risk the gold proof takes, kept as it is on main.
func TestEvmBlockLowerBoundDetectorKeepsGoldBlockVerdictWhenNoBlockHasBody(t *testing.T) {
	detector := blockDetector(blocksByKind(headBlock, bareLookingExceptGold(blockBareHeader)))

	assert.Equal(t, headBlock, detectBlockBound(t, detector))
}

// A body search that cannot read the head settles nothing: the plain bound stands for the
// cycle, and the next one asks the blocks near it again and proves the upstream. The head is
// read once for the plain search, then the three attempts of the body search fail.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearPlainBoundAgainAfterFailedBodySearch(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectLatest(connector, fmt.Sprintf("0x%x", headBlock))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("eth_blockNumber"))).
		Return(blockAnswers[blockError]).
		Times(3)
	detector := blockDetector(serveBlocksByKind(connector, headBlock, nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))))
	require.Equal(t, headersFrom, detectBlockBound(t, detector))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// failingBetween is kindOf on a node whose requests for the blocks above from, up to to, fail
// while failing is set.
func failingBetween(from, to int64, failing *bool, kindOf func(int64) blockKind) func(int64) blockKind {
	return func(h int64) blockKind {
		if *failing && h > from && h <= to {
			return blockError
		}
		return kindOf(h)
	}
}

// Blocks that could not be read settle nothing: the plain bound stands for the cycle, and the
// next one proves the node restored from a snapshot. In the first cycle its block requests
// fail in a part of the chain that only one step of the proof reads: the probes of the body
// search under the head, or the blocks asked between the bare header and the body bound.
// Some failed probes are enough, with a body found by the others: the search takes a failed
// probe for a block without a body and stops there, above the first body.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearPlainBoundAgainAfterUnreadBlocks(t *testing.T) {
	tests := []struct {
		name            string
		failingFrom, to int64
	}{
		{"body probes fail", 900_000, headBlock},
		{"some body probes fail, others find a body", 990_000, 997_000},
		{"blocks below the body bound fail", 600_000, 800_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			failing := true
			node := nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))
			detector := blockDetector(blocksByKind(headBlock, failingBetween(tc.failingFrom, tc.to, &failing, node)))
			require.Equal(t, headersFrom, detectBlockBound(t, detector))

			failing = false

			assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
		})
	}
}

// For an upstream proven near its plain bound, body probes that fail do not clear the verdict:
// the cycle fails where a cleared verdict would answer the plain bound. It fails too when
// only some probes do and the others find a body: the search would answer a bound above the
// first body, and the bound processor does not lower a bound again.
func TestEvmBlockLowerBoundDetectorFailsCycleOfProvenUpstreamWhenBodyProbesFail(t *testing.T) {
	tests := []struct {
		name            string
		failingFrom, to int64
	}{
		{"body probes fail", 900_000, headBlock},
		{"some body probes fail, others find a body", 990_000, 997_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			failing := false
			node := nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))
			detector := blockDetector(blocksByKind(headBlock, failingBetween(tc.failingFrom, tc.to, &failing, node)))
			require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

			failing = true
			_, err := detector.DetectLowerBound(context.Background())

			assert.Error(t, err)
		})
	}
}

// A body probe that answers on a repeat is no failure: the upstream is proven in the same
// cycle. The first request for a block between 990000 and 997000 fails once, and that is a
// probe of the body search.
func TestEvmBlockLowerBoundDetectorTakesBodyProbeAnsweredOnRepeat(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
			h, ok := evmBlockHeight(r)
			return ok && h > 990_000 && h <= 997_000
		})).
		Return(blockAnswers[blockError]).
		Once()
	detector := blockDetector(serveBlocksByKind(connector, headBlock, nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// The head seeds the choice of the blocks asked between the bare header and the body bound:
// at the next head the detector asks other ones. On this node the blocks from 510000 to
// 860000 are asked for nothing else: the plain search stays below them on its way to
// headersFrom, and the body search above.
func TestEvmBlockLowerBoundDetectorAsksOtherBlocksBelowBodyBoundAtAnotherHead(t *testing.T) {
	askedAt := func(head int64) []int64 {
		connector := blocksByKind(head, nullBelow(headersFrom, bareHeadersBelow(bodiesFrom)))
		require.Equal(t, bodiesFrom, detectBlockBound(t, blockDetector(connector)))
		return blocksRequestedBetween(connector, 510_000, 860_000)
	}

	assert.NotEqual(t, askedAt(headBlock), askedAt(headBlock+1))
}

// The blocks asked to confirm a bare header lie above it, in the proving cycle and in the
// next one alike: on a node restored from a late snapshot none falls in the missing part of
// the chain, where a block tells nothing. Nothing else reads the blocks from 100000 to 499000
// here: the plain search asks 500000 first and goes up from there.
func TestEvmBlockLowerBoundDetectorAsksBlocksAboveBareHeaderToConfirmIt(t *testing.T) {
	connector := blocksByKind(headBlock, nullBelow(lateHeadersFrom, bareHeadersBelow(bodiesFrom)))
	detector := blockDetectorOn(evmChain(), connector)
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	assert.Empty(t, blocksRequestedBetween(connector, 100_000, 499_000))
}

// Bodies that reach the bare header or go below it contradict it: the block is not a pruned
// one, and no verdict is taken. Blocks 1 and 2 are empty here and block 3 looks bare; the
// body search passes it by on its way down and answers 1. The next cycle asks no block of
// the upper quarter of the chain, where only the body search goes.
func TestEvmBlockLowerBoundDetectorTakesNoVerdictWhenBodiesReachBelowBareHeader(t *testing.T) {
	const upperQuarter = headBlock * 3 / 4
	connector := blocksByKind(headBlock, func(h int64) blockKind {
		switch h {
		case 1, 2:
			return blockEmpty
		case 3:
			return blockBareHeader
		default:
			return blockWithTxs
		}
	})
	detector := blockDetectorOn(evmChain(), connector)
	require.Equal(t, int64(1), detectBlockBound(t, detector))
	askedUnderHead := blockRequestsAbove(connector, upperQuarter)

	detectBlockBound(t, detector)

	assert.Equal(t, askedUnderHead, blockRequestsAbove(connector, upperQuarter))
}

// The first header of a node restored from a snapshot may be the only one without a body, as
// on a live ronin-saigon node: the bound is the next block. No block lies between that header
// and the body bound, and none past the bound is asked for transactions.
func TestEvmBlockLowerBoundDetectorFindsFirstBlockWithBodyAfterSingleBareHeader(t *testing.T) {
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, nullBelow(headersFrom, bareHeadersBelow(headersFrom+1))))

	assert.Equal(t, headersFrom+1, detectBlockBound(t, detector))
}

// The proof asks the blocks up to evmBareHeaderProofWindow past the plain bound and passes the
// empty ones. A bare header further away is not reached, and the plain bound stays.
func TestEvmBlockLowerBoundDetectorAsksBlocksUpToProofWindowPastPlainBound(t *testing.T) {
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

// A block near the plain bound that gives no answer decides nothing, and the next one is
// asked: the upstream is proven in the same cycle. The plain bound is 1 here, as block 0
// answers with its header.
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

// When the plain bound moves, the blocks near the new one are asked. The node is pruned at
// headersFrom first and serves bodies from there; then its first block is laterHeadersFrom,
// with bare headers from it on.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearPlainBoundThatMoved(t *testing.T) {
	const laterHeadersFrom = headersFrom + 50_000
	kindOf := nullBelow(headersFrom, func(int64) blockKind { return blockWithTxs })
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, headersFrom, detectBlockBound(t, detector))

	kindOf = nullBelow(laterHeadersFrom, bareHeadersBelow(bodiesFrom))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// A plain bound whose blocks proved nothing is asked again after the recheck interval. An
// archive node gets replaced behind its URL by one that keeps bare headers from genesis: the
// plain bound stays 1, and the blocks near it are bare headers now.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearUnchangedPlainBoundAgainAfterRecheckInterval(t *testing.T) {
	const recheckInterval = 20 * time.Millisecond
	kindOf := func(int64) blockKind { return blockWithTxs }
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	detector.SetBareHeaderRecheckInterval(recheckInterval)
	require.Equal(t, int64(1), detectBlockBound(t, detector))

	kindOf = bareHeadersBelow(bodiesFrom)
	time.Sleep(2 * recheckInterval)

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// The tests from here to ...StopsAskingBlocksNearPlainBoundWhenContextEnds count the requests
// of single blocks and lean on the order of the plain search probes. On a chain of 1000000
// blocks that answers from block 0, it asks 14, 6, 2, 0 and 1 in the first cycle and 15, 7,
// 3 and 1 in the second: the other blocks up to 21 are asked by the proof alone.

// A block with transactions at the plain bound ends the proof: bodies start there, and the
// blocks after it are not asked.
func TestEvmBlockLowerBoundDetectorEndsProofAtBlockWithTransactions(t *testing.T) {
	connector := blocksByKind(headBlock, func(int64) blockKind { return blockWithTxs })
	detector := blockDetectorOn(evmChain(), connector)

	detectBlockBound(t, detector)

	assert.Zero(t, blockRequests(connector, 1+askedPastBound))
}

// Blocks near the plain bound that proved nothing are not asked again while the bound stays:
// an archive node of a chain that starts with empty blocks pays for the window once. Null is
// an answer too: a missing block among them leaves the bound checked.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearUnchangedPlainBoundOnce(t *testing.T) {
	const firstBlockWithTxs = 1_000
	const missingBlock = 5
	tests := []struct {
		name    string
		missing bool
	}{
		{"empty blocks", false},
		{"empty blocks and a missing one", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			connector := blocksByKind(headBlock, func(h int64) blockKind {
				switch {
				case tc.missing && h == missingBlock:
					return blockAbsent
				case h < firstBlockWithTxs:
					return blockEmpty
				default:
					return blockWithTxs
				}
			})
			detector := blockDetectorOn(evmChain(), connector)
			detectBlockBound(t, detector)

			detectBlockBound(t, detector)

			assert.Equal(t, 1, blockRequests(connector, 1+askedPastBound))
		})
	}
}

// A failed request near the plain bound leaves the bound to be asked again. Block 3 is a
// bare header among empty blocks, and its first request fails: that one comes from the proof.
// The next cycle proves the upstream.
func TestEvmBlockLowerBoundDetectorAsksBlocksNearPlainBoundAgainAfterFailedRequest(t *testing.T) {
	const bareHeaderAt = 3
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(isBlockRequest(bareHeaderAt))).
		Return(blockAnswers[blockError]).
		Once()
	detector := blockDetectorOn(evmChain(), serveBlocksByKind(connector, headBlock, func(h int64) blockKind {
		if h <= 1+askedPastBound && h != bareHeaderAt {
			return blockEmpty
		}
		return bareHeadersBelow(bodiesFrom)(h)
	}))
	require.Equal(t, int64(1), detectBlockBound(t, detector))

	assert.Equal(t, bodiesFrom, detectBlockBound(t, detector))
}

// The proof stops asking when the context ends. Blocks 3 and 4 are asked by the proof alone:
// the context ends on block 3, and block 4 is not asked.
func TestEvmBlockLowerBoundDetectorStopsAskingBlocksNearPlainBoundWhenContextEnds(t *testing.T) {
	const contextEndsAt = 3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(isBlockRequest(contextEndsAt))).
		Run(func(mock.Arguments) { cancel() }).
		Return(blockAnswers[blockEmpty])
	detector := blockDetectorOn(evmChain(), serveBlocksByKind(connector, headBlock, func(int64) blockKind { return blockEmpty }))

	_, _ = detector.DetectLowerBound(ctx)

	assert.Zero(t, blockRequests(connector, contextEndsAt+1))
}

// Once the blocks near the plain bound have proven the upstream, a cycle in which they prove
// nothing keeps the verdict. The node drops its oldest headers here and the first ones left
// are of empty blocks: the plain search would answer the first of them.
func TestEvmBlockLowerBoundDetectorKeepsVerdictOfBlocksNearPlainBound(t *testing.T) {
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

// The verdict of the blocks near the plain bound goes when the body search meets no block
// with its body: the plain search decides again. Every block of the node looks bare in the
// second cycle. In the third its first blocks are empty: the body search would leave them
// out, as they come right after the missing ones, and answer the first block with transactions.
func TestEvmBlockLowerBoundDetectorDropsVerdictOfBlocksNearPlainBoundWhenNoBlockHasBody(t *testing.T) {
	const emptyBlocks = 5
	kindOf := nullBelow(headersFrom, bareHeadersBelow(bodiesFrom))
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	kindOf = nullBelow(headersFrom, bareLookingExceptGold(blockBareHeader))
	assert.Equal(t, headersFrom, detectBlockBound(t, detector))

	kindOf = nullBelow(headersFrom, func(h int64) blockKind {
		if h < headersFrom+emptyBlocks {
			return blockKlaytnEmpty
		}
		return blockWithTxs
	})
	assert.Equal(t, headersFrom, detectBlockBound(t, detector))
}

// The verdict goes too when blocks between its bare header and the bound of the body search
// show transactions. The node restored from a late snapshot is proven first; then its blocks
// stand whole, every third looking bare, and the body search stops at one of those near the
// head.
func TestEvmBlockLowerBoundDetectorDropsVerdictOfBlocksNearPlainBoundWhenBlocksBelowBodyBoundHaveBodies(t *testing.T) {
	kindOf := nullBelow(lateHeadersFrom, bareHeadersBelow(bodiesFrom))
	detector := blockDetectorOn(evmChain(), blocksByKind(headBlock, func(h int64) blockKind { return kindOf(h) }))
	require.Equal(t, bodiesFrom, detectBlockBound(t, detector))

	kindOf = nullBelow(lateHeadersFrom, everyNthLooksBare(3))

	assert.Equal(t, lateHeadersFrom, detectBlockBound(t, detector))
}

func isBlockRequest(height int64) func(protocol.RequestHolder) bool {
	return func(r protocol.RequestHolder) bool {
		h, ok := evmBlockHeight(r)
		return ok && h == height
	}
}

func isGoldBlockRequest(r protocol.RequestHolder) bool {
	return isBlockRequest(goldBlock)(r)
}

// countBlockRequests counts the requests for the blocks whose height matches.
func countBlockRequests(connector *mocks.ConnectorMock, match func(height int64) bool) int {
	count := 0
	for _, call := range connector.Calls {
		request, ok := call.Arguments.Get(1).(protocol.RequestHolder)
		if !ok {
			continue
		}
		if h, isBlock := evmBlockHeight(request); isBlock && match(h) {
			count++
		}
	}
	return count
}

func blockRequests(connector *mocks.ConnectorMock, height int64) int {
	return countBlockRequests(connector, func(h int64) bool { return h == height })
}

func blockRequestsAbove(connector *mocks.ConnectorMock, height int64) int {
	return countBlockRequests(connector, func(h int64) bool { return h > height })
}

// blocksRequestedBetween lists the heights above from and below to in the order they were
// asked.
func blocksRequestedBetween(connector *mocks.ConnectorMock, from, to int64) []int64 {
	var heights []int64
	countBlockRequests(connector, func(h int64) bool {
		if h > from && h < to {
			heights = append(heights, h)
		}
		return false
	})
	return heights
}

func goldBlockRequests(connector *mocks.ConnectorMock) int {
	return blockRequests(connector, goldBlock)
}

// A failed gold block request is repeated, three attempts in all, and the first answer ends
// them. An upstream proven on a repeat gets its bound in the same cycle; after three failures
// the gold block gives the cycle no verdict. The chain starts with empty blocks, so the
// blocks near the plain bound give none either and the plain search answers 1.
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
