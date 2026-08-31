package flow

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
)

type SubscriptionRequestProcessor struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	engine             subengine.Engine
	subCtx             *SubCtx
	registry           *rating.RatingRegistry
	localSubs          config.LocalSubSettings
}

func NewSubscriptionRequestProcessor(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	engine subengine.Engine,
	subCtx *SubCtx,
	registry *rating.RatingRegistry,
	localSubs config.LocalSubSettings,
) *SubscriptionRequestProcessor {
	return &SubscriptionRequestProcessor{
		chain:              chain,
		upstreamSupervisor: upstreamSupervisor,
		engine:             engine,
		subCtx:             subCtx,
		registry:           registry,
		localSubs:          localSubs,
	}
}

func (s *SubscriptionRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	responses := make(chan *protocol.ResponseHolderWrapper)

	go func() {
		defer close(responses)

		// send never parks the goroutine on a consumer that stopped reading:
		// once the request ctx is gone, so is every reader of responses.
		send := func(wrapper *protocol.ResponseHolderWrapper) bool {
			select {
			case responses <- wrapper:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// Only subscription methods are served here: unsubscribe calls and
		// plain methods can arrive flagged as subscriptions (the emerald server
		// flags every native-subscribe request), and must be refused.
		if request.SpecMethod() == nil || !request.SpecMethod().IsSubscribe() {
			send(totalFailureWrapper(request, fmt.Errorf("%s is not a subscription method", request.Method())))
			return
		}
		framing := s.framing()

		execCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// All subscriptions route through the per-chain aggregation engine so
		// identical (method+params+selector) subscriptions share a single
		// upstream source instead of opening one node subscription per client
		// (gRPC streams opt out via a per-request key, see resolveSource).
		// The shared source emits events only - each client's framing decides
		// how (and whether) the subscription is announced to the client.
		key, builder, filter := resolveSource(s.chain, s.upstreamSupervisor, request, upstreamStrategy, s.registry, s.engine, s.localSubs)
		sub, err := s.engine.Subscribe(key, builder)
		if err != nil {
			send(totalFailureWrapper(request, err))
			return
		}
		defer sub.Unsubscribe()

		ack, err := framing.begin(request, cancel)
		if err != nil {
			send(totalFailureWrapper(request, err))
			return
		}
		if ack != nil && !send(ack) {
			return
		}

		for {
			select {
			case r, ok := <-sub.Events:
				if !ok {
					// The shared source ended. An error frame is a terminal failure
					// (node disconnect, param reject, slow consumer) - the real
					// cause is preserved rather than collapsed into a generic
					// error. An end frame is the clean completion of a bounded
					// stream, announced with its trailers. No frame at all means
					// this client detached.
					if terminal := sub.Terminal(); terminal != nil {
						send(terminalWrapper(request, terminal))
					}
					return
				}
				// Per-client logs filtering: the shared logs source carries every
				// log of the chain; drop the ones this client did not subscribe to.
				// Never filter terminal frames (handled above; they carry no Message).
				if filter != nil && !filter.Matches(r.GetParsedEvent()) {
					continue
				}
				wrapper := &protocol.ResponseHolderWrapper{
					UpstreamId: responseUpstreamId(r),
					RequestId:  request.Id(),
					Response:   framing.event(request, r),
				}
				if !send(wrapper) {
					return
				}
			case <-execCtx.Done():
				return
			}
		}
	}()

	return &SubscriptionResponse{responses}
}

// framing picks how this client sees the subscription: bare event payloads
// for result-only consumers (the gRPC ingress, the emerald server), the
// JSON-RPC ack + notification envelope otherwise (the WS server).
func (s *SubscriptionRequestProcessor) framing() subFraming {
	if s.subCtx.IsSubscriptionResultOnly() {
		return resultOnlyFraming{}
	}
	return &jsonRpcFraming{chain: s.chain, subCtx: s.subCtx}
}

func responseUpstreamId(r protocol.SubResponse) string {
	if id := r.GetUpstreamId(); id != "" {
		return id
	}
	return NoUpstream
}

// terminalWrapper turns the frame that ended a subscription into the client's
// final response - the terminal error, or a non-event end frame for a clean
// completion - keeping the transport metadata it carried (gRPC trailers).
func terminalWrapper(request protocol.RequestHolder, terminal protocol.SubResponse) *protocol.ResponseHolderWrapper {
	headers, trailers := protocol.ResponseMetadata(terminal)
	if terminal.GetError() == nil {
		return &protocol.ResponseHolderWrapper{
			UpstreamId: responseUpstreamId(terminal),
			RequestId:  request.Id(),
			Response:   protocol.NewSubscriptionEndResponse(request.Id()).WithResponseHeaders(headers).WithResponseTrailers(trailers),
		}
	}
	wrapper := totalFailureWrapper(request, terminal.GetError())
	if replyErr, ok := wrapper.Response.(*protocol.ReplyError); ok {
		replyErr.WithResponseHeaders(headers).WithResponseTrailers(trailers)
	}
	return wrapper
}
