package flow

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// SubCtx is the per-client-connection subscription state and the owner of the
// client dialect: how a subscription is presented and how the client
// unsubscribes. The WS server creates one per connection by chain type; the
// gRPC ingress and the emerald server use the result-only one.
type SubCtx interface {
	// Reserve runs in the ingress for a subscribe request, in frame order,
	// before the flow starts. The channel dialect files the subscription here,
	// so a client's cancel can never arrive before the entry exists.
	Reserve(ctx context.Context, request protocol.RequestHolder)
	// Framing presents one subscription to this client.
	Framing() subFraming
	// Unsubscribe serves the client's unsubscribe call: it cancels every
	// subscription filed under key and returns the reply the client gets for
	// that call.
	Unsubscribe(request protocol.RequestHolder, key string) ProcessedResponse
	// Exists reports whether key has live subscriptions.
	Exists(key string) bool
}

// NewSubCtx picks the dialect by chain type: go-jsonrpc channels for a
// Celestia chain, the JSON-RPC subscription model everywhere else.
func NewSubCtx(chain chains.Chain) SubCtx {
	if chains.GetChain(chain.String()).Type == chains.Celestia {
		return newChannelSubCtx()
	}
	return &baseSubCtx{chain: chain, subs: utils.NewCMap[string, context.CancelFunc]()}
}

// NewResultOnlySubCtx is for consumers that carry their own framing (the gRPC
// ingress, the emerald server): bare events, no ack, no client unsubscribe.
func NewResultOnlySubCtx() SubCtx {
	return &baseSubCtx{resultOnly: true, subs: utils.NewCMap[string, context.CancelFunc]()}
}

// baseSubCtx is the JSON-RPC subscription model (eth_subscribe, Solana and
// Substrate subscriptions): the ack carries a subscription id nodecore
// generates, and the client unsubscribes with that id. Generated ids never
// collide, so one key holds exactly one subscription.
type baseSubCtx struct {
	chain      chains.Chain
	resultOnly bool
	subs       *utils.CMap[string, context.CancelFunc]
}

// Reserve is a no-op: the key is the subscription id nodecore generates at ack
// time, and an eth-style client cannot unsubscribe before it holds that id.
func (b *baseSubCtx) Reserve(context.Context, protocol.RequestHolder) {}

func (b *baseSubCtx) Framing() subFraming {
	if b.resultOnly {
		return resultOnlyFraming{}
	}
	return &jsonRpcFraming{chain: b.chain, subCtx: b}
}

// Unsubscribe answers `true` whether or not the id was known, as eth nodes do.
func (b *baseSubCtx) Unsubscribe(request protocol.RequestHolder, key string) ProcessedResponse {
	if cancel, ok := b.subs.LoadAndDelete(key); ok {
		cancel()
	}
	return &UnaryResponse{
		&protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), ResultTrue, request.RequestType()),
		},
	}
}

func (b *baseSubCtx) Exists(key string) bool {
	_, ok := b.subs.Load(key)
	return ok
}

func (b *baseSubCtx) addSub(subId string, cancel context.CancelFunc) {
	b.subs.Store(subId, cancel)
}

var _ SubCtx = (*baseSubCtx)(nil)
