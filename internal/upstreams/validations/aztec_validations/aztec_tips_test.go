package aztec_validations_test

import (
	"context"
	"sync"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aztec_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func aztecChain() chains.Chain {
	return chains.GetChain("aztec-mainnet").Chain
}

func validTipsResponse() protocol.ResponseHolder {
	body := []byte(`{"proposed":{"number":7,"hash":"0x7"}}`)
	return protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc)
}

func methodNotFoundResponse(method string) protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponseWithError(
		protocol.ResponseErrorWithData(protocol.NoSupportedMethod, "Method not found: "+method, nil),
	)
}

func genericErrorResponse() protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponseWithError(
		protocol.ResponseErrorWithData(1, "boom", nil),
	)
}

// recordingConnector is a fake ApiConnector that records every method it is
// asked for and answers from a swappable response table, so tests can assert
// exactly which tips method was called and in what order.
type recordingConnector struct {
	*mocks.ConnectorMock
	mu        sync.Mutex
	methods   []string
	responses map[string]protocol.ResponseHolder
}

func newRecordingConnector(responses map[string]protocol.ResponseHolder) *recordingConnector {
	return &recordingConnector{
		ConnectorMock: mocks.NewConnectorMock(),
		responses:     responses,
	}
}

func (c *recordingConnector) SendRequest(_ context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods = append(c.methods, request.Method())
	if resp, ok := c.responses[request.Method()]; ok {
		return resp
	}
	return methodNotFoundResponse(request.Method())
}

func (c *recordingConnector) setResponses(responses map[string]protocol.ResponseHolder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = responses
}

func (c *recordingConnector) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...)
}

// (а) node_getL2Tips answers => it is used, no second call.
func TestTipsResolverPrimarySucceedsWithoutFallback(t *testing.T) {
	conn := newRecordingConnector(map[string]protocol.ResponseHolder{
		aztec_validations.MethodGetL2Tips: validTipsResponse(),
	})
	resolver := aztec_validations.NewTipsMethodResolver()

	response, err := resolver.FetchTips(context.Background(), conn, aztecChain())

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t, []string{aztec_validations.MethodGetL2Tips}, conn.calls())
}

// (б) node_getL2Tips => -32601, node_getChainTips => valid: result is correct,
// and the next poll goes straight to node_getChainTips (the method is cached).
func TestTipsResolverFallsBackToChainTipsAndCaches(t *testing.T) {
	conn := newRecordingConnector(map[string]protocol.ResponseHolder{
		aztec_validations.MethodGetL2Tips:    methodNotFoundResponse(aztec_validations.MethodGetL2Tips),
		aztec_validations.MethodGetChainTips: validTipsResponse(),
	})
	resolver := aztec_validations.NewTipsMethodResolver()

	response, err := resolver.FetchTips(context.Background(), conn, aztecChain())
	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t,
		[]string{aztec_validations.MethodGetL2Tips, aztec_validations.MethodGetChainTips},
		conn.calls(),
		"first poll must probe node_getL2Tips then fall back to node_getChainTips",
	)

	response2, err := resolver.FetchTips(context.Background(), conn, aztecChain())
	assert.Nil(t, err)
	assert.False(t, response2.HasError())
	assert.Equal(t,
		[]string{
			aztec_validations.MethodGetL2Tips,
			aztec_validations.MethodGetChainTips,
			aztec_validations.MethodGetChainTips,
		},
		conn.calls(),
		"second poll must use the cached node_getChainTips without re-probing node_getL2Tips",
	)
}

// (в) on a non-method-not-found error there is NO fallback: one call, error is
// returned as-is.
func TestTipsResolverNoFallbackOnOtherError(t *testing.T) {
	conn := newRecordingConnector(map[string]protocol.ResponseHolder{
		aztec_validations.MethodGetL2Tips: genericErrorResponse(),
	})
	resolver := aztec_validations.NewTipsMethodResolver()

	response, err := resolver.FetchTips(context.Background(), conn, aztecChain())

	assert.Nil(t, err)
	assert.True(t, response.HasError())
	assert.Equal(t, 1, response.GetError().Code)
	assert.Equal(t, []string{aztec_validations.MethodGetL2Tips}, conn.calls())
}

// The cached method self-heals in both directions: a node upgraded to v5 makes
// us switch L2Tips -> ChainTips, and a node rolled back makes us switch back.
func TestTipsResolverSelfHealsBothDirections(t *testing.T) {
	conn := newRecordingConnector(map[string]protocol.ResponseHolder{
		aztec_validations.MethodGetL2Tips:    methodNotFoundResponse(aztec_validations.MethodGetL2Tips),
		aztec_validations.MethodGetChainTips: validTipsResponse(),
	})
	resolver := aztec_validations.NewTipsMethodResolver()

	// Node is on v5: we discover and cache node_getChainTips.
	_, _ = resolver.FetchTips(context.Background(), conn, aztecChain())

	// Node is rolled back to v4: node_getChainTips now 404s, node_getL2Tips works.
	conn.setResponses(map[string]protocol.ResponseHolder{
		aztec_validations.MethodGetChainTips: methodNotFoundResponse(aztec_validations.MethodGetChainTips),
		aztec_validations.MethodGetL2Tips:    validTipsResponse(),
	})

	response, err := resolver.FetchTips(context.Background(), conn, aztecChain())
	assert.Nil(t, err)
	assert.False(t, response.HasError())

	// And the next poll uses the re-cached node_getL2Tips directly.
	_, _ = resolver.FetchTips(context.Background(), conn, aztecChain())
	assert.Equal(t,
		[]string{
			aztec_validations.MethodGetL2Tips,    // initial probe
			aztec_validations.MethodGetChainTips, // fall back, cache ChainTips
			aztec_validations.MethodGetChainTips, // cached probe, now 404s
			aztec_validations.MethodGetL2Tips,    // heal back, cache L2Tips
			aztec_validations.MethodGetL2Tips,    // cached, used directly
		},
		conn.calls(),
	)
}
