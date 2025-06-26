package flow

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
)

type ProcessedResponse interface {
	response()
}

type UnaryResponse struct {
	ResponseWrapper *protocol.ResponseHolderWrapper
}

func (u *UnaryResponse) response() {
}

var _ ProcessedResponse = (*UnaryResponse)(nil)

type SubscriptionResponse struct {
	ResponseWrappers chan *protocol.ResponseHolderWrapper
}

func (s *SubscriptionResponse) response() {
}

var _ ProcessedResponse = (*SubscriptionResponse)(nil)

type RequestProcessor interface {
	ProcessRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) ProcessedResponse
}

type UnaryRequestProcessor struct {
	chain              chains.Chain
	cacheProcessor     caches.CacheProcessor
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func NewUnaryRequestProcessor(chain chains.Chain, cacheProcessor caches.CacheProcessor, upstreamSupervisor upstreams.UpstreamSupervisor) *UnaryRequestProcessor {
	return &UnaryRequestProcessor{
		chain:              chain,
		cacheProcessor:     cacheProcessor,
		upstreamSupervisor: upstreamSupervisor,
	}
}

func (u *UnaryRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	var response *protocol.ResponseHolderWrapper
	var err error
	fromCache := false // don't store responses from cache

	if specs.IsSubscribeMethod(chains.GetMethodSpecNameByChain(u.chain), request.Method()) {
		err = protocol.ClientError(fmt.Errorf("unable to process a subscription request %s", request.Method()))
		response = &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
		}
	} else {
		result, ok := u.cacheProcessor.Receive(ctx, u.chain, request)
		if ok {
			fromCache = true
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), result, request.RequestType()),
			}
		} else {
			response, err = u.upstreamSupervisor.
				GetExecutor().
				WithContext(ctx).
				GetWithExecution(func(exec failsafe.Execution[*protocol.ResponseHolderWrapper]) (*protocol.ResponseHolderWrapper, error) {
					upstreamId, err := upstreamStrategy.SelectUpstream(request)
					if err != nil {
						return nil, protocol.ExecutionError(exec.Hedges(), err)
					}
					responseHolder, err := sendUnaryRequest(ctx, u.upstreamSupervisor.GetUpstream(upstreamId), request)
					if err != nil {
						return nil, protocol.ExecutionError(exec.Hedges(), err)
					}
					return responseHolder, nil
				})
			if err != nil {
				response = &protocol.ResponseHolderWrapper{
					UpstreamId: NoUpstream,
					RequestId:  request.Id(),
					Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
				}
			}
		}
	}

	if !fromCache && !response.Response.HasError() && !response.Response.HasStream() {
		go u.cacheProcessor.Store(ctx, u.chain, request, response.Response.ResponseResult())
	}

	return &UnaryResponse{ResponseWrapper: response}
}

func getUnaryCapableConnector(upstream *upstreams.Upstream, requestType protocol.RequestType) connectors.ApiConnector {
	switch requestType {
	case protocol.Rest:
		return upstream.GetConnector(protocol.RestConnector)
	case protocol.JsonRpc:
		connector := upstream.GetConnector(protocol.JsonRpcConnector)
		if connector == nil {
			connector = upstream.GetConnector(protocol.WsConnector)
		}
		return connector
	default:
		return nil
	}
}

func sendUnaryRequest(
	ctx context.Context,
	upstream *upstreams.Upstream,
	request protocol.RequestHolder,
) (*protocol.ResponseHolderWrapper, error) {
	var apiConnector connectors.ApiConnector

	switch request.(type) {
	case *protocol.BaseUpstreamRequest:
		apiConnector = getUnaryCapableConnector(upstream, request.RequestType())
	}
	if apiConnector == nil {
		return nil, fmt.Errorf("unable to process a %s request", request.RequestType())
	}

	response := apiConnector.SendRequest(ctx, request)

	return &protocol.ResponseHolderWrapper{
		RequestId:  request.Id(),
		UpstreamId: upstream.Id,
		Response:   response,
	}, nil
}

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
					if r.SubId == "" {
						s.subCtx.AddSub(protocol.ResultAsString(r.Message), cancel)
					}
					wrapper := &protocol.ResponseHolderWrapper{
						UpstreamId: upstreamId,
						RequestId:  request.Id(),
						Response:   protocol.NewSubscriptionEventResponse(request.Id(), r.Event),
					}
					responses <- wrapper
				}
			case <-execCtx.Done():
				return
			}
		}
	}()

	return &SubscriptionResponse{responses}
}
