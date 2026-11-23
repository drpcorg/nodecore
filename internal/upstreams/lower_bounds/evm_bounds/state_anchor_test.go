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
	// callsFailWithoutState: asked for a block it has no state of, eth_call fails and only
	// eth_getBalance says so (besu: "Internal error" and a null balance)
	callsFailWithoutState bool
	// failures is how many times the upstream fails a request before it answers it
	failures map[upstreamRequest]int

	// contractCalls counts the calls to the contract and plainProbes the plain state probes
	contractCalls, plainProbes int
	// failed counts the failed answers to a request
	failed map[upstreamRequest]int
}

// upstreamRequest names a request to the history contract or for a header by the block it is
// sent for.
type upstreamRequest struct {
	method string
	block  int64
}

// everyTime is the number of failures of a request the upstream never answers.
const everyTime = -1

// fails reports a request the upstream has to fail this time.
func (c *historyChain) fails(request upstreamRequest) bool {
	left := c.failures[request]
	if left > 0 {
		c.failures[request] = left - 1
	}
	return left != 0
}

func (c *historyChain) fail(request upstreamRequest) protocol.ResponseHolder {
	if c.failed == nil {
		c.failed = map[upstreamRequest]int{}
	}
	c.failed[request]++
	return failedAnswer()
}

func failedAnswer() protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))
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
		if header := (upstreamRequest{method: request.Method(), block: height}); c.fails(header) {
			return c.fail(header)
		}
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
		height := requestBlock(body, 1)
		contractCall := upstreamRequest{method: request.Method(), block: height}
		toContract := strings.EqualFold(requestParam(body, 0, "to"), historyContract)
		served, ok := c.stateOf(height)
		if !ok {
			switch {
			case !c.callsFailWithoutState:
				return pruned
			case toContract:
				return c.fail(contractCall)
			default:
				return failedAnswer()
			}
		}
		if !toContract {
			// the state override probe
			c.plainProbes++
			return evmOK(`"0x0000000000000000000000000000000000000000000000000000000000000001"`)
		}
		c.contractCalls++
		if c.contractFails || c.fails(contractCall) {
			return c.fail(contractCall)
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

// firstAskedBelowSnapshot is the first block the search from the head asks below block
// 800_000 on its way down from historyHead.
const firstAskedBelowSnapshot = 738_880

// A block whose anchor could not be asked has no verdict. The plain probes would give it
// one: they take the state of the snapshot block for the state of the block. So a block that
// keeps failing is a block without state.
func TestEvmStateBoundIsAtTheSnapshotWhenACallOfTheAnchorKeepsFailingOnABlockBelowIt(t *testing.T) {
	const fakeFrom, snapshot = 600_000, 800_000
	failing := map[string]upstreamRequest{
		"the history contract at the block":     {method: "eth_call", block: firstAskedBelowSnapshot},
		"the header of the anchor block":        {method: "eth_getBlockByNumber", block: firstAskedBelowSnapshot - historyWindow + 1},
		"the history contract one window below": {method: "eth_call", block: firstAskedBelowSnapshot - historyWindow},
	}
	for name, request := range failing {
		t.Run(name, func(t *testing.T) {
			upstream := &historyChain{
				activation: historyActivation,
				stateOf:    stateOfSnapshotBetween(fakeFrom, snapshot),
				failures:   map[upstreamRequest]int{request: everyTime},
			}
			detector := fastEvm(evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream))

			assert.Equal(t, stateAndTraceAt(snapshot-1), stateBounds(t, detector))
			assert.NotZero(t, upstream.failed[request], "the search has sent the request that fails")
		})
	}
}

// The block is asked again: one failed call does not cut the history of an archive at it.
func TestEvmStateBoundOfAnArchiveReachesGenesisThroughAFailedCallToTheHistoryContract(t *testing.T) {
	contractCall := upstreamRequest{method: "eth_call", block: firstAskedBelowSnapshot}
	upstream := &historyChain{
		activation: historyActivation,
		stateOf:    realState,
		failures:   map[upstreamRequest]int{contractCall: 1},
	}
	detector := fastEvm(evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream))

	assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))
	assert.NotZero(t, upstream.failed[contractCall], "the search has sent the request that fails")
}

// Besu on Linea: below its state window the node fails the call to the contract, and the
// plain probes find no state there. Such a block is not asked again: 30 attempts with the
// backoff of the search are 24 minutes for each of them.
func TestEvmStateBoundOfAPrunedUpstreamDoesNotRetryBlocksWhereTheAnchorFailsAndThePlainProbesFindNoState(t *testing.T) {
	const firstBlock = 990_000
	upstream := &historyChain{
		activation:            historyActivation,
		callsFailWithoutState: true,
		stateOf: func(height int64) (int64, bool) {
			return height, height >= firstBlock
		},
	}
	detector := evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, upstream)
	// a block that is asked again is asked three times
	detector.SetSearchRetryPolicy(3, time.Millisecond, time.Millisecond)

	assert.Equal(t, stateAndTraceAt(firstBlock), stateBounds(t, detector))
	require.NotEmpty(t, upstream.failed, "the search has asked blocks without state")
	for request, times := range upstream.failed {
		assert.Equalf(t, 1, times, "block %d is asked once", request.block)
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
	unanswered := map[string]historyChain{
		"the call to the contract fails": {contractFails: true},
		"the anchor block has no header": {firstHeader: historyHead},
	}
	for name, upstream := range unanswered {
		t.Run(name, func(t *testing.T) {
			upstream.activation, upstream.stateOf = historyActivation, stateOfSnapshotBetween(fakeFrom, snapshot)
			detector := fastEvm(evm_bounds.NewEvmStateLowerBoundDetector("id", evmChain(), time.Second, &upstream))

			assert.Equal(t, stateAndTraceAt(1), stateBounds(t, detector))

			upstream.contractFails, upstream.firstHeader = false, 0
			assert.Equal(t, stateAndTraceAt(snapshot-1), stateBounds(t, detector))
		})
	}
}
