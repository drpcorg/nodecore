package evm_bounds_test

import (
	"context"
	"fmt"
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
	// blockBareHeader is a block whose body the node pruned: the header commits to transactions
	blockBareHeader
	// blockZeroRoot and blockNoRoot have no transactions and a root that commits to nothing
	blockZeroRoot
	blockNoRoot
	blockFails
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
	blockAbsent:     evmOK(`null`),
	blockWithTxs:    evmOK(`{"number":"0x1","transactionsRoot":"0x6199cc9681e6c1793ed9b28d8214ea42642b5dfd89a9b80b1cc20046714dfe0e","transactions":["0xaa"]}`),
	blockEmpty:      evmOK(`{"number":"0x1","transactionsRoot":"` + emptyTxRoot + `","transactions":[]}`),
	blockBareHeader: evmOK(liveBareHeader),
	blockZeroRoot:   evmOK(`{"number":"0x1","transactionsRoot":"` + zeroTxRoot + `","transactions":[]}`),
	blockNoRoot:     evmOK(`{"number":"0x1","transactions":[]}`),
	blockFails:      protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom")),
}

// blocksNode serves a head and, for every height, the answer kindOf picks.
func blocksNode(head int64, kindOf func(int64) blockKind) *mocks.ConnectorMock {
	connector := mocks.NewConnectorMock()
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

const goldBlock = int64(1)

// goldChain has its gold transaction in goldBlock, as BSC does.
func goldChain() *chains.ConfiguredChain {
	return evmChainWithGold(&chains.GoldLowerBound{Block: uint64(goldBlock), Hash: goldTxHash}, nil)
}

func blockBound(t *testing.T, detector *evm_bounds.EvmLowerBoundDetector) int64 {
	t.Helper()
	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, protocol.BlockBound, result[0].Type)
	return result[0].Bound
}

// reth with pruned bodies answers every old block with its bare header. The block bound is the
// first block that still has its body, not 1.
func TestEvmBlockLowerBoundDetectorFindsBodiesStartBehindBareHeaders(t *testing.T) {
	const head, bodiesFrom = int64(1_000_000), int64(1_000_000 - 14_350)
	connector := blocksNode(head, func(h int64) blockKind {
		if h >= bodiesFrom {
			return blockWithTxs
		}
		return blockBareHeader
	})

	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))

	assert.Equal(t, bodiesFrom, blockBound(t, detector))
}

// On a chain whose transactionsRoot covers more than the listed transactions, ordinary blocks
// look like bare headers. The gold block has its transactions, so the upstream is not taken for
// a pruned one and keeps bound 1.
func TestEvmBlockLowerBoundDetectorTrustsHeadersWhenGoldBlockHasItsBody(t *testing.T) {
	connector := blocksNode(1_000_000, func(h int64) blockKind {
		if h == goldBlock {
			return blockWithTxs
		}
		return blockBareHeader
	})

	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))

	assert.Equal(t, int64(1), blockBound(t, detector))
}

// A gold block without transactions proves a pruned body only when its root commits to some.
// Otherwise the upstream stays on the plain search, which here answers 1; the body search
// would stop at the bare headers.
func TestEvmBlockLowerBoundDetectorNeedsCommittingRootToProveBareHeaders(t *testing.T) {
	for name, goldKind := range map[string]blockKind{
		"empty trie root": blockEmpty,
		"zero root":       blockZeroRoot,
		"no root":         blockNoRoot,
	} {
		t.Run(name, func(t *testing.T) {
			connector := blocksNode(1_000_000, func(h int64) blockKind {
				if h == goldBlock {
					return goldKind
				}
				return blockBareHeader
			})

			detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))

			assert.Equal(t, int64(1), blockBound(t, detector))
		})
	}
}

// A block that really has no transactions looks the same with and without its body, so it
// decides nothing: the nearest block with transactions does. Every even block is empty here,
// on both sides of the bodies start.
func TestEvmBlockLowerBoundDetectorSkipsEmptyBlocksInBodySearch(t *testing.T) {
	const head, bodiesFrom = int64(1_000_000), int64(1_000_000 - 14_351)
	connector := blocksNode(head, func(h int64) blockKind {
		switch {
		case h%2 == 0:
			return blockEmpty
		case h >= bodiesFrom:
			return blockWithTxs
		default:
			return blockBareHeader
		}
	})

	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))

	assert.Equal(t, bodiesFrom, blockBound(t, detector))
}

// Once the upstream is known to keep bare headers, a failed gold block request does not send
// it back to the search that trusts headers and answers 1.
func TestEvmBlockLowerBoundDetectorKeepsBareHeadersVerdictWhenGoldRequestFails(t *testing.T) {
	const head, bodiesFrom = int64(1_000_000), int64(1_000_000 - 14_350)
	goldFails := false
	connector := blocksNode(head, func(h int64) blockKind {
		switch {
		case h == goldBlock && goldFails:
			return blockFails
		case h >= bodiesFrom:
			return blockWithTxs
		default:
			return blockBareHeader
		}
	})
	detector := fastEvm(evm_bounds.NewEvmBlockLowerBoundDetector("id", goldChain(), time.Second, connector))
	require.Equal(t, bodiesFrom, blockBound(t, detector))

	goldFails = true

	assert.Equal(t, bodiesFrom, blockBound(t, detector))
}
