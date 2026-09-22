package flow

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// subFraming is how a subscription is presented to one client. The shared
// pipeline (source resolution, engine, filtering, terminal handling) is the
// same for every client; only the announcement and the per-event wrapping
// differ.
type subFraming interface {
	// attach runs before the source is opened and returns the context the
	// subscription lives on, cancelled by the client's unsubscribe: the one
	// reserved by the ingress for the channel dialect, a fresh child of ctx
	// for the others.
	attach(ctx context.Context, request protocol.RequestHolder) (context.Context, error)
	// begin runs once the source is attached. It may announce the subscription
	// to the client (returned wrapper, nil for none).
	begin(request protocol.RequestHolder) (*protocol.ResponseHolderWrapper, error)
	// event wraps one upstream frame for the client.
	event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder
}

// jsonRpcFraming is the WS JSON-RPC presentation: a subscribe ack carrying a
// fresh client subscription id, then notification envelopes referencing it.
type jsonRpcFraming struct {
	chain  chains.Chain
	subCtx *baseSubCtx
	cancel context.CancelFunc
	subId  json.RawMessage
}

func (f *jsonRpcFraming) attach(ctx context.Context, request protocol.RequestHolder) (context.Context, error) {
	if err := needsSubscriptionInfo(request); err != nil {
		return nil, err
	}
	subCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	return subCtx, nil
}

func (f *jsonRpcFraming) begin(request protocol.RequestHolder) (*protocol.ResponseHolderWrapper, error) {
	subId, err := nextSubscriptionJson(isSolana(f.chain))
	if err != nil {
		log.Error().Err(err).Msgf("failed to generate subscription id for %s", request.Method())
		return nil, protocol.SubscribeTotalFailureError()
	}
	f.subId = subId
	f.subCtx.addSub(protocol.ResultAsString(subId), f.cancel)
	return &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewWsJsonRpcResponse(request.Id(), subId, nil),
	}, nil
}

func (f *jsonRpcFraming) event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder {
	return protocol.NewJsonRpcSubscriptionEventResponse(request.Id(), request.SpecMethod().Subscription.Method, r.GetMessage(), f.subId)
}

// resultOnlyFraming is the presentation for consumers that carry their own
// framing (the gRPC ingress, the emerald server): no ack, no subscription id,
// each event is the bare payload plus the transport metadata the frame
// carried (gRPC headers/trailers). Its consumers end a subscription by
// cancelling the request ctx, so the subscription lives on that ctx as is.
type resultOnlyFraming struct{}

func (resultOnlyFraming) attach(ctx context.Context, _ protocol.RequestHolder) (context.Context, error) {
	return ctx, nil
}

func (resultOnlyFraming) begin(protocol.RequestHolder) (*protocol.ResponseHolderWrapper, error) {
	return nil, nil
}

func (resultOnlyFraming) event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder {
	headers, trailers := protocol.ResponseMetadata(r)
	return protocol.NewSubscriptionEventResponse(request.Id(), r.GetMessage()).WithResponseHeaders(headers).WithResponseTrailers(trailers)
}

// channelFraming is the go-jsonrpc channel presentation (celestia-node
// clients): the ack carries the per-connection channel id the channelSubCtx
// reserved when the ingress read the subscribe frame, and events are
// xrpc.ch.val notifications with params [channelId, value]. The xrpc.ch.close
// that answers a cancel is written by channelSubCtx.
type channelFraming struct {
	subCtx    *channelSubCtx
	channelId uint64
}

func (f *channelFraming) attach(_ context.Context, request protocol.RequestHolder) (context.Context, error) {
	if err := needsSubscriptionInfo(request); err != nil {
		return nil, err
	}
	// only a JSON-RPC request carries the client's own id, which is what the
	// reservation is filed under and what the client puts into xrpc.cancel
	realId, ok := request.(protocol.RealIdHolder)
	if !ok {
		return nil, fmt.Errorf("%s needs the client's JSON-RPC request id for a channel subscription", request.Method())
	}
	ctx, channelId, ok := f.subCtx.attach(realId.RealId())
	if !ok {
		return nil, fmt.Errorf("%s was not reserved for request %s", request.Method(), realId.RealId())
	}
	f.channelId = channelId
	return ctx, nil
}

func (f *channelFraming) begin(request protocol.RequestHolder) (*protocol.ResponseHolderWrapper, error) {
	return &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewWsJsonRpcResponse(request.Id(), json.RawMessage(strconv.FormatUint(f.channelId, 10)), nil),
	}, nil
}

func (f *channelFraming) event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder {
	return protocol.NewChannelSubscriptionEventResponse(request.Id(), request.SpecMethod().Subscription.Method, r.GetMessage(), f.channelId)
}

// needsSubscriptionInfo refuses a request whose method has no JSON-RPC
// subscription block: the notification envelope needs its method name, and a
// gRPC stream method (IsSubscribe by call type) has none.
func needsSubscriptionInfo(request protocol.RequestHolder) error {
	if request.SpecMethod().Subscription == nil {
		return fmt.Errorf("%s has no JSON-RPC subscription info", request.Method())
	}
	return nil
}

func isSolana(chain chains.Chain) bool {
	return chain == chains.SOLANA || chain == chains.SOLANA_DEVNET || chain == chains.SOLANA_TESTNET
}

func nextSubscriptionJson(isNumber bool) (json.RawMessage, error) {
	if isNumber {
		subscriptionId, err := nextSubscriptionId(6)
		if err != nil {
			return nil, err
		}
		subId := json.RawMessage(fmt.Sprintf("%d", binary.BigEndian.Uint64(append(subscriptionId, byte(0), byte(0)))))
		return subId, nil
	}
	subscriptionId, err := nextSubscriptionId(20)
	if err != nil {
		return nil, err
	}
	subId := json.RawMessage(fmt.Sprintf("\"0x%s\"", hex.EncodeToString(subscriptionId)))
	return subId, nil
}

func nextSubscriptionId(n int) ([]byte, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}
