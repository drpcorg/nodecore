package aztec_validations

import (
	"context"
	"strings"
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	// MethodGetL2Tips is the pre-v5 chain-tips method. Mainnet and every node
	// below v5 only know this one, so it stays the method we try first and the
	// only one ever sent to an unupgraded node.
	MethodGetL2Tips = "node_getL2Tips"
	// MethodGetChainTips is the v5 rename of node_getL2Tips. v5 nodes only know
	// this one and answer node_getL2Tips with -32601 "method not found".
	MethodGetChainTips = "node_getChainTips"
)

// TipsMethodResolver fetches Aztec chain tips while transparently bridging the
// v5 rename node_getL2Tips -> node_getChainTips.
//
// It sends the method this upstream is known to support (node_getL2Tips by
// default, so mainnet and older nodes are never probed with an extra round
// trip) and only on a -32601 "method not found" response retries with the
// other method, caching whichever one the node accepts. Any other error is
// returned unchanged, with no fallback. Because the cached method is replaced
// whenever the current one stops working, it self-heals in both directions if
// a node is upgraded or rolled back at runtime.
//
// A single resolver is meant to be shared per upstream (e.g. by the head poller
// and the health validator) so a method that 404s is not re-probed on every
// poll and the logs are not spammed.
type TipsMethodResolver struct {
	mu     sync.Mutex
	method string // last method that worked; empty => use MethodGetL2Tips
}

func NewTipsMethodResolver() *TipsMethodResolver {
	return &TipsMethodResolver{}
}

// FetchTips sends the chain-tips request, applying the bidirectional
// node_getL2Tips <-> node_getChainTips fallback. The returned ResponseHolder is
// the upstream's response to whichever method was accepted; callers parse it
// exactly as before. The returned error is non-nil only when the request could
// not be built (RPC-level errors arrive via ResponseHolder.HasError()).
func (r *TipsMethodResolver) FetchTips(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (protocol.ResponseHolder, error) {
	primary := r.preferredMethod()
	response, err := sendTipsRequest(ctx, connector, chain, primary)
	if err != nil {
		return nil, err
	}
	if !isMethodNotFound(response) {
		// Success or a genuine error - no fallback either way. `primary` is
		// already what preferredMethod() returns, so there is nothing new to
		// cache; the hot path (mainnet, where node_getL2Tips always works) thus
		// takes the lock only once, for the read.
		return response, nil
	}

	// The preferred method is not supported by this node; switch to the other
	// one and remember it if it works. This is the only place the cached method
	// actually changes, so it is the only place we take the write lock.
	fallback := otherTipsMethod(primary)
	fallbackResponse, err := sendTipsRequest(ctx, connector, chain, fallback)
	if err != nil {
		return nil, err
	}
	if !fallbackResponse.HasError() {
		r.remember(fallback)
	}
	return fallbackResponse, nil
}

func (r *TipsMethodResolver) preferredMethod() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.method == "" {
		return MethodGetL2Tips
	}
	return r.method
}

func (r *TipsMethodResolver) remember(method string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method = method
}

func sendTipsRequest(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
	method string,
) (protocol.ResponseHolder, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, []any{}, chain)
	if err != nil {
		return nil, err
	}
	return connector.SendRequest(ctx, request), nil
}

func otherTipsMethod(method string) string {
	if method == MethodGetChainTips {
		return MethodGetL2Tips
	}
	return MethodGetChainTips
}

// isMethodNotFound reports whether the upstream rejected the call because the
// method does not exist (JSON-RPC -32601) - the cue to try the renamed method.
// It also matches the textual "method not found", since some nodes surface the
// rename with a non-standard error code.
func isMethodNotFound(response protocol.ResponseHolder) bool {
	if response == nil || !response.HasError() {
		return false
	}
	respErr := response.GetError()
	if respErr == nil {
		return false
	}
	if respErr.Code == protocol.NoSupportedMethod {
		return true
	}
	return strings.Contains(strings.ToLower(respErr.Message), "method not found")
}
