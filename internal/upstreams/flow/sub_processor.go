package flow

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type SubscriptionRequestProcessor struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	engine             subengine.Engine
	subCtx             *SubCtx
}

func NewSubscriptionRequestProcessor(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	engine subengine.Engine,
	subCtx *SubCtx,
) *SubscriptionRequestProcessor {
	return &SubscriptionRequestProcessor{
		chain:              chain,
		upstreamSupervisor: upstreamSupervisor,
		engine:             engine,
		subCtx:             subCtx,
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

		if request.SpecMethod() == nil || request.SpecMethod().Subscription == nil {
			responses <- failureWrapper(request, errors.New("no subscription info"))
			return
		}

		method := request.SpecMethod().Subscription.Method

		execCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// All subscriptions route through the per-chain aggregation engine so
		// identical (method+params+selector) subscriptions share a single
		// upstream source instead of opening one node subscription per client.
		// The shared source emits events only - each client allocates its own
		// client-facing subscription id below, independent of the single
		// upstream subscription id.
		key := subscriptionKey(request)
		events, unsub, err := s.engine.Subscribe(key, sourceBuilder(s.chain, s.upstreamSupervisor, request, upstreamStrategy))
		if err != nil {
			responses <- failureWrapper(request, err)
			return
		}
		defer unsub()

		subId, err := nextSubscriptionJson(isSolana(s.chain))
		if err != nil {
			log.Error().Err(err).Msgf("failed to generate subscription id for %s", request.Method())
			responses <- &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.WsTotalFailureError(), request.RequestType()),
			}
			return
		}
		s.subCtx.AddSub(protocol.ResultAsString(subId), cancel)
		responses <- &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewSubscriptionMessageEventResponse(request.Id(), subId),
		}

		for {
			select {
			case r, ok := <-events:
				if !ok {
					return
				}
				if r.Error != nil {
					responses <- &protocol.ResponseHolderWrapper{
						UpstreamId: responseUpstreamId(r),
						RequestId:  request.Id(),
						Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.WsTotalFailureError(), request.RequestType()),
					}
					return
				}
				var subResponse protocol.ResponseHolder
				if s.subCtx.IsSubscriptionResultOnly() {
					subResponse = protocol.NewSubscriptionResultEventResponse(request.Id(), r.Message)
				} else {
					subResponse = protocol.NewSubscriptionMethodResultResponse(request.Id(), method, r.Message, subId)
				}
				responses <- &protocol.ResponseHolderWrapper{
					UpstreamId: responseUpstreamId(r),
					RequestId:  request.Id(),
					Response:   subResponse,
				}
			case <-execCtx.Done():
				return
			}
		}
	}()

	return &SubscriptionResponse{responses}
}

func failureWrapper(request protocol.RequestHolder, err error) *protocol.ResponseHolderWrapper {
	return &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
	}
}

func responseUpstreamId(r *protocol.WsResponse) string {
	if r.UpstreamId != "" {
		return r.UpstreamId
	}
	return NoUpstream
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
