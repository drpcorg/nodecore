package evm_bounds_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/evm_bounds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	historyContract = "0x0000f90827f1c53a10cb7a02335b175320002935"
	historyWindow   = 8191

	historyHead       = 1_000_000
	historyActivation = 470_000
)

// historyChain is an upstream of a chain with the EIP-2935 history contract. Its headers are
// always right. stateOf names the block whose state the upstream answers a block with; ok is
// false for a pruned state.
type historyChain struct {
	connectors.ApiConnector
	stateOf func(height int64) (served int64, ok bool)
	// deployed is the first block with the contract, and activation the first one that stored
	// the hash of its parent there
	deployed, activation int64
	// firstHeader is the oldest block the upstream has a header for
	firstHeader int64
	// callsOnTopOfBlock: the client runs eth_call as in the next block, after the contract has
	// stored the hash of the block itself (the nodes of Gnosis do)
	callsOnTopOfBlock bool
	// foreignBelow: for the blocks below it the contract keeps hashes their headers do not have
	foreignBelow int64
	// contractFails: every call to the contract fails
	contractFails bool

	// contractCalls counts the calls to the contract and plainProbes the plain state probes
	contractCalls, plainProbes int
}

func realState(height int64) (int64, bool) {
	return height, true
}

// stateOfSnapshotBetween is an upstream restored from a snapshot: it has the real state from
// the snapshot block on, answers the blocks from fakeFrom up to it with the state of the
// snapshot block, and has the real state below again.
func stateOfSnapshotBetween(fakeFrom, snapshot int64) func(int64) (int64, bool) {
	return func(height int64) (int64, bool) {
		if height >= fakeFrom && height < snapshot {
			return snapshot, true
		}
		return height, true
	}
}

func headerHash(height int64) string {
	return fmt.Sprintf("0xb10c%060x", height)
}

func (c *historyChain) SendRequest(_ context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	body, _ := request.Body()
	pruned := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("missing trie node"))
	switch request.Method() {
	case "eth_blockNumber":
		return evmOK(fmt.Sprintf(`"0x%x"`, historyHead))
	case "eth_getBlockByNumber":
		height := requestBlock(body, 0)
		if height < c.firstHeader {
			return evmOK(`null`)
		}
		// upper case: the letter case of a hash means nothing
		hash := "0x" + strings.ToUpper(headerHash(height)[2:])
		return evmOK(fmt.Sprintf(`{"number":"0x%x","hash":"%s","transactions":[]}`, height, hash))
	case "eth_getBalance":
		if _, ok := c.stateOf(requestBlock(body, 1)); !ok {
			return pruned
		}
		return evmOK(`"0x1"`)
	case "eth_call":
		served, ok := c.stateOf(requestBlock(body, 1))
		if !ok {
			return pruned
		}
		if !strings.EqualFold(requestParam(body, 0, "to"), historyContract) {
			// the state override probe
			c.plainProbes++
			return evmOK(`"0x0000000000000000000000000000000000000000000000000000000000000001"`)
		}
		c.contractCalls++
		if c.contractFails {
			return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))
		}
		asked, _ := strconv.ParseInt(strings.TrimLeft(strings.TrimPrefix(requestParam(body, 0, "data"), "0x"), "0"), 16, 64)
		if c.callsOnTopOfBlock {
			served++
		}
		return evmOK(`"` + c.keptHash(asked, served) + `"`)
	}
	return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("unexpected " + request.Method()))
}

// keptHash is the answer of the contract for a block number in the state of block served: the
// hash of the latest block below served that shares the slot of the asked one.
func (c *historyChain) keptHash(asked, served int64) string {
	if served < c.deployed {
		return "0x"
	}
	kept := served - 1 - (served-1-asked)%historyWindow
	switch {
	case kept < c.activation-1:
		return "0x" + strings.Repeat("0", 64)
	case kept < c.foreignBelow:
		return fmt.Sprintf("0xf00d%060x", kept)
	default:
		return headerHash(kept)
	}
}

func requestParam(body []byte, path ...any) string {
	node, err := sonic.Get(body, append([]any{"params"}, path...)...)
	if err != nil {
		return ""
	}
	value, _ := node.String()
	return value
}

func requestBlock(body []byte, index int) int64 {
	tag := requestParam(body, index)
	if tag == "latest" {
		return historyHead
	}
	height, _ := strconv.ParseInt(strings.TrimPrefix(tag, "0x"), 16, 64)
	return height
}

func stateBounds(t *testing.T, detector *evm_bounds.EvmLowerBoundDetector) map[protocol.LowerBoundType]int64 {
	t.Helper()
	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	return boundsByType(result)
}

func stateAndTraceAt(bound int64) map[protocol.LowerBoundType]int64 {
	return map[protocol.LowerBoundType]int64{protocol.StateBound: bound, protocol.TraceBound: bound}
}

// The upstream of Zendesk #4040: an OP node restored from a snapshot that keeps answering the
// older blocks, and has real history below them again.
func TestEvmStateBoundIsAtTheSnapshotOfAnUpstreamServingItsStateForOlderBlocks(t *testing.T) {
	const fakeFrom, snapshot = 600_000, 800_000
	tests := []struct {
		name              string
		callsOnTopOfBlock bool
		bound             int64
	}{
		// the state of the next block is the one the anchor cannot tell from the real one
		{"eth_call runs in the block", false, snapshot - 1},
		{"eth_call runs on top of the block", true, snapshot},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &historyChain{
				activation:        historyActivation,
				stateOf:           stateOfSnapshotBetween(fakeFrom, snapshot),
				callsOnTopOfBlock: test.callsOnTopOfBlock,
			}
			detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

			assert.Equal(t, stateAndTraceAt(test.bound), stateBounds(t, detector))
			assert.Zero(t, upstream.plainProbes, "the anchor decides a block without the plain probes")
		})
	}
}

// The search from the head asks blocks 738_880 and 476_736 on its way to genesis.
func TestEvmStateBoundOfAnArchiveReachesGenesisThroughBlocksTheHistoryContractSaysNothingAbout(t *testing.T) {
	const activation = 740_000
	deployments := map[string]int64{
		// as on Ethereum, where the contract was deployed long before the fork
		"the contract has not stored a hash yet": 0,
		"the contract does not exist yet":        activation,
	}
	for name, deployed := range deployments {
		t.Run(name, func(t *testing.T) {
			upstream := &historyChain{deployed: deployed, activation: activation, stateOf: realState}
			detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

			assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))
		})
	}
}

// A node synced from a snapshot has neither the state nor the headers below it, so the hashes
// the contract keeps in its first blocks have no header to check them against.
func TestEvmStateBoundOfAPrunedUpstreamIsItsFirstBlockWithState(t *testing.T) {
	const firstBlock = 990_000
	upstream := &historyChain{activation: historyActivation, firstHeader: firstBlock, stateOf: func(height int64) (int64, bool) {
		return height, height >= firstBlock
	}}
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

	assert.Equal(t, stateAndTraceAt(firstBlock), stateBounds(t, detector))
}

// missingBetween is an upstream that has lost the state of the blocks from holeFrom up to holeTo.
func missingBetween(holeFrom, holeTo int64) func(int64) (int64, bool) {
	return func(height int64) (int64, bool) {
		return height, height < holeFrom || height >= holeTo
	}
}

// Upstreams without an anchor to trust keep the search they had: from genesis, which takes the
// first old block that answers for the bound.
func TestEvmStateBoundIsSearchedFromGenesisWithoutATrustedAnchor(t *testing.T) {
	const holeFrom, holeTo = 600_000, 900_000
	t.Run("no history contract", func(t *testing.T) {
		upstream := &historyChain{deployed: historyHead + 1, stateOf: missingBetween(holeFrom, holeTo)}
		detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

		assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))
		assert.Equal(t, 1, upstream.contractCalls, "the contract is asked for the head block only")
	})
	t.Run("the history contract disagrees with the headers at the head", func(t *testing.T) {
		upstream := &historyChain{activation: historyActivation, foreignBelow: historyHead + 1, stateOf: missingBetween(holeFrom, holeTo)}
		detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

		assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))
	})
}

// Cronos: the contract agrees with the headers at the head and keeps other hashes for the
// blocks below some old one.
func TestEvmStateBoundOfAnArchiveReachesGenesisThroughBlocksWhereTheHistoryContractDisagreesWithTheHeaders(t *testing.T) {
	upstream := &historyChain{activation: historyActivation, foreignBelow: 900_000, stateOf: realState}
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

	assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))
}

func TestEvmStateAnchorVerdictIsKeptForTheRecheckIntervalAndAskedAgainAfterIt(t *testing.T) {
	const fakeFrom, snapshot = 600_000, 800_000
	upstream := &historyChain{activation: historyHead + 1, stateOf: stateOfSnapshotBetween(fakeFrom, snapshot)}
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

	// before the fork the contract stores nothing: nothing tells the fake range from real state
	assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))

	// the fork
	upstream.activation = historyActivation
	assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector), "the verdict is kept for the interval")

	detector.SetStateAnchorRecheckInterval(0)
	assert.Equal(t, stateAndTraceAt(snapshot-1), stateBounds(t, detector), "the verdict is asked again after the interval")
}

func TestEvmStateAnchorCheckWithoutAnAnswerIsRepeatedInTheNextCycle(t *testing.T) {
	const fakeFrom, snapshot = 600_000, 800_000
	upstream := &historyChain{activation: historyActivation, stateOf: stateOfSnapshotBetween(fakeFrom, snapshot), contractFails: true}
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)

	assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))

	upstream.contractFails = false
	assert.Equal(t, stateAndTraceAt(snapshot-1), stateBounds(t, detector))
}
