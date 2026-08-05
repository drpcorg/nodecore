package polkadot_bounds_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/polkadot_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newDetector(connector connectors.ApiConnector) *polkadot_bounds.PolkadotLowerBoundDetector {
	detector := polkadot_bounds.NewPolkadotLowerBoundDetector(
		"id", chains.GetChain("polkadot").Chain, 5*time.Second, connector,
	)
	// Negligible backoff so the binary search does not sleep through the test.
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)
	return detector
}

func jsonRpcResult(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc)
}

// pruningNode is a hand-written ApiConnector standing in for a node that retains
// state from stateFrom upward. It is hand-written rather than a
// mocks.ConnectorMock because the binary search visits heights that cannot be
// enumerated up front, and ConnectorMock casts its configured return value
// statically (`args.Get(0).(protocol.ResponseHolder)` in
// pkg/test_utils/mocks/connector.go), so a per-height computed response cannot
// be expressed with testify. Embedding the interface supplies the methods the
// detector never calls.
type pruningNode struct {
	connectors.ApiConnector

	latest    int64
	stateFrom int64
}

func (p *pruningNode) SendRequest(_ context.Context, req protocol.RequestHolder) protocol.ResponseHolder {
	switch req.Method() {
	case "chain_getHeader":
		return jsonRpcResult(fmt.Sprintf(`{"parentHash":"0xaa","number":"0x%x"}`, p.latest))
	case "chain_getBlockHash":
		// The height is encoded into the hash so state_getMetadata can recover it.
		return jsonRpcResult(fmt.Sprintf(`"0x%016x"`, heightFromHexParam(req)))
	case "state_getMetadata":
		height := heightFromHexParam(req)
		if height < p.stateFrom {
			return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(
				-32000, fmt.Sprintf("State already discarded for 0x%016x", height), nil,
			))
		}
		return jsonRpcResult(`"0x6d657461"`)
	}
	return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(
		-32601, "Method not found: "+req.Method(), nil,
	))
}

// heightFromHexParam pulls the single 0x-prefixed hex argument out of a request
// body like {"jsonrpc":"2.0","id":1,"method":"...","params":["0x384"]}, and
// returns -1 when there is none.
func heightFromHexParam(req protocol.RequestHolder) int64 {
	body, err := req.Body()
	if err != nil {
		return -1
	}
	raw := string(body)
	start := strings.Index(raw, `["0x`)
	if start < 0 {
		return -1
	}
	rest := raw[start+4:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return -1
	}
	height, err := strconv.ParseInt(rest[:end], 16, 64)
	if err != nil {
		return -1
	}
	return height
}

func TestPolkadotLowerBoundSupportedTypesAndPeriod(t *testing.T) {
	detector := newDetector(&pruningNode{latest: 1000, stateFrom: 1})

	assert.ElementsMatch(t, []protocol.LowerBoundType{protocol.StateBound}, detector.SupportedTypes())
	assert.Equal(t, 5*time.Minute, detector.Period())
}

func TestPolkadotLowerBoundFindsPrunedBoundary(t *testing.T) {
	result, err := newDetector(&pruningNode{latest: 1000, stateFrom: 900}).
		DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, protocol.StateBound, result[0].Type)
	assert.Equal(t, int64(900), result[0].Bound)
}

func TestPolkadotLowerBoundArchiveNodeConvergesLow(t *testing.T) {
	result, err := newDetector(&pruningNode{latest: 1000, stateFrom: 1}).
		DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].Bound)
}

// nullHashNode answers null for chain_getBlockHash below a floor, which is what a
// blocks-pruning node does for a block it dropped. That is absence of data, so it
// must narrow the search immediately rather than burn retries and then be
// misclassified.
type nullHashNode struct {
	connectors.ApiConnector

	latest    int64
	blockFrom int64
	probes    atomic.Int64
}

func (n *nullHashNode) SendRequest(_ context.Context, req protocol.RequestHolder) protocol.ResponseHolder {
	switch req.Method() {
	case "chain_getHeader":
		return jsonRpcResult(fmt.Sprintf(`{"parentHash":"0xaa","number":"0x%x"}`, n.latest))
	case "chain_getBlockHash":
		n.probes.Add(1)
		if heightFromHexParam(req) < n.blockFrom {
			return jsonRpcResult(`null`)
		}
		return jsonRpcResult(fmt.Sprintf(`"0x%016x"`, heightFromHexParam(req)))
	case "state_getMetadata":
		return jsonRpcResult(`"0x6d657461"`)
	}
	return protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32601, "nope", nil))
}

func TestPolkadotLowerBoundTreatsNullBlockHashAsNoData(t *testing.T) {
	node := &nullHashNode{latest: 1000, blockFrom: 900}

	result, err := newDetector(node).DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(900), result[0].Bound)
	// A retried null would multiply this by the retry attempts; the binary search
	// over 0..1000 needs only ~11 probes plus the converge re-check.
	assert.Less(t, node.probes.Load(), int64(20), "null answers were retried instead of narrowing")
}

// A null metadata result at a resolvable hash is also no-data, not retained state.
func TestPolkadotLowerBoundTreatsNullMetadataAsNoData(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getHeader"
	})).Return(jsonRpcResult(`{"parentHash":"0xaa","number":"0x64"}`))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getBlockHash"
	})).Return(jsonRpcResult(`"0xabc"`))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "state_getMetadata"
	})).Return(jsonRpcResult(`null`))

	// Nothing is retained anywhere, so the search cannot confirm a bound and must
	// report failure rather than publish a bogus 1.
	_, err := newDetector(connector).DetectLowerBound(context.Background())
	assert.Error(t, err)
}

// A latest-height failure must not be reported as a bound: the processor keeps
// the previous value instead.
func TestPolkadotLowerBoundLatestHeightFailure(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	result, err := newDetector(connector).DetectLowerBound(context.Background())
	assert.Nil(t, result)
	assert.Error(t, err)
}
