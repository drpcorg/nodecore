package flow

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
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

		for {
			select {
			case r, ok := <-subResp.ResponseChan():
				if ok {
					var subResponse protocol.ResponseHolder
					if r.SubId == "" {
						s.subCtx.AddSub(protocol.ResultAsString(r.Message), cancel)
						subResponse = protocol.NewSubscriptionMessageEventResponse(request.Id(), r.Message)
					} else {
						subResponse = protocol.NewSubscriptionEventResponse(request.Id(), r.Event)
					}
					wrapper := &protocol.ResponseHolderWrapper{
						UpstreamId: upstreamId,
						RequestId:  request.Id(),
						Response:   subResponse,
					}
					responses <- wrapper
				} else {
					return
				}
			case <-execCtx.Done():
				return
			}
		}
	}()

	return &SubscriptionResponse{responses}
}
