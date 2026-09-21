package flow

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// subFraming is how a subscription is presented to one client. The shared
// pipeline (source resolution, engine, filtering, terminal handling) is the
// same for every client; only the announcement and the per-event wrapping
// differ.
type subFraming interface {
	// begin runs once the source is attached. It may announce the subscription
	// to the client (returned wrapper, nil for none) and register it for a later
	// unsubscribe via cancel.
	begin(request protocol.RequestHolder, cancel context.CancelFunc) (*protocol.ResponseHolderWrapper, error)
	// event wraps one upstream frame for the client.
	event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder
}

// jsonRpcFraming is the WS JSON-RPC presentation: a subscribe ack carrying a
// fresh client subscription id, then notification envelopes referencing it.
type jsonRpcFraming struct {
	chain  chains.Chain
	subCtx *SubCtx
	subId  json.RawMessage
}

func (f *jsonRpcFraming) begin(request protocol.RequestHolder, cancel context.CancelFunc) (*protocol.ResponseHolderWrapper, error) {
	// the notification envelope needs the JSON-RPC notification method; a gRPC
	// stream method (IsSubscribe by call type) has none
	if request.SpecMethod().Subscription == nil {
		return nil, fmt.Errorf("%s has no JSON-RPC subscription info", request.Method())
	}
	subId, err := nextSubscriptionJson(isSolana(f.chain))
	if err != nil {
		log.Error().Err(err).Msgf("failed to generate subscription id for %s", request.Method())
		return nil, protocol.SubscribeTotalFailureError()
	}
	f.subId = subId
	f.subCtx.AddSub(protocol.ResultAsString(subId), cancel)
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
// carried (gRPC headers/trailers).
type resultOnlyFraming struct{}

func (resultOnlyFraming) begin(protocol.RequestHolder, context.CancelFunc) (*protocol.ResponseHolderWrapper, error) {
	return nil, nil
}

func (resultOnlyFraming) event(request protocol.RequestHolder, r protocol.SubResponse) protocol.ResponseHolder {
	headers, trailers := protocol.ResponseMetadata(r)
	return protocol.NewSubscriptionEventResponse(request.Id(), r.GetMessage()).WithResponseHeaders(headers).WithResponseTrailers(trailers)
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
