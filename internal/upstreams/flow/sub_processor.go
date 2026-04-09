package flow

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type SubscriptionRequestProcessor struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
	subCtx             *SubCtx
}

func NewSubscriptionRequestProcessor(upstreamSupervisor upstreams.UpstreamSupervisor, subCtx *SubCtx) *SubscriptionRequestProcessor {
	return &SubscriptionRequestProcessor{upstreamSupervisor: upstreamSupervisor, subCtx: subCtx}
}

func (s *SubscriptionRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	responses := make(chan *protocol.ResponseHolderWrapper)

	go func() {
		defer close(responses)
		var response *protocol.ResponseHolderWrapper

		if request.SpecMethod() == nil || request.SpecMethod().Subscription == nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), errors.New("no subscription info"), request.RequestType()),
			}
			responses <- response
			return
		}

		method := request.SpecMethod().Subscription.Method

		//TODO: it might be a good idea to select an upstream with a ws (or other sub) head connector
		// and receive updates from it in order to reduce client's costs
		// otherwise choose any upstream with a sub capability
		upstreamId, err := upstreamStrategy.SelectUpstream(request)
		if err != nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
			}
			responses <- response
			return
		}

		upstream := s.upstreamSupervisor.GetUpstream(upstreamId)

		// however there could be other connectors as well
		// like http connector to support SSE
		wsConn := upstream.GetConnector(protocol.WsConnector)

		execCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		var stateChan chan protocol.SubscribeConnectorState
		connectorStatesSub := wsConn.SubscribeStates(
			fmt.Sprintf("%s_%s_request_%s_%d", upstream.GetId(), request.Method(), request.Id(), time.Now().UnixNano()),
		)
		if connectorStatesSub != nil {
			stateChan = connectorStatesSub.Events
			defer connectorStatesSub.Unsubscribe()
		}

		subResp, err := wsConn.Subscribe(execCtx, request)
		if err != nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
			}
			responses <- response
			return
		}
		var currentSubId json.RawMessage

		for {
			select {
			case state, ok := <-stateChan:
				if ok {
					if state == protocol.WsDisconnected {
						responses <- &protocol.ResponseHolderWrapper{
							UpstreamId: upstreamId,
							RequestId:  request.Id(),
							Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.WsTotalFailureError(), request.RequestType()),
						}
						return
					}
				}
			case r, ok := <-subResp.ResponseChan():
				if !ok {
					return
				}
				var subResponse protocol.ResponseHolder
				if r.SubId == "" {
					subId, err := nextSubscriptionJson(isSolana(upstream.GetChain()))
					if err != nil {
						log.Error().Err(err).Msgf("failed to generate subscription id for %s", request.Method())
						responses <- &protocol.ResponseHolderWrapper{
							UpstreamId: upstreamId,
							RequestId:  request.Id(),
							Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.WsTotalFailureError(), request.RequestType()),
						}
						return
					}
					currentSubId = subId
					s.subCtx.AddSub(protocol.ResultAsString(subId), cancel)
					subResponse = protocol.NewSubscriptionMessageEventResponse(request.Id(), subId)
				} else {
					if s.subCtx.IsSubscriptionResultOnly() {
						subResponse = protocol.NewSubscriptionResultEventResponse(request.Id(), r.Message)
					} else {
						subResponse = protocol.NewSubscriptionMethodResultResponse(request.Id(), method, r.Message, currentSubId)
					}
				}
				wrapper := &protocol.ResponseHolderWrapper{
					UpstreamId: upstreamId,
					RequestId:  request.Id(),
					Response:   subResponse,
				}
				responses <- wrapper
			case <-execCtx.Done():
				return
			}
		}
	}()

	return &SubscriptionResponse{responses}
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
