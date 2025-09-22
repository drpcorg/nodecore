package flow

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

const maxBytes = 5

type StickyRequestProcessor struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func NewStickyRequestProcessor(chain chains.Chain, upstreamSupervisor upstreams.UpstreamSupervisor) *StickyRequestProcessor {
	return &StickyRequestProcessor{
		chain:              chain,
		upstreamSupervisor: upstreamSupervisor,
	}
}

func (s *StickyRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	var response *protocol.ResponseHolderWrapper
	var err error

	if specs.IsStickySendMethod(request.SpecMethod()) {
		methodParam := request.ParseParams(ctx)
		switch param := methodParam.(type) {
		case *specs.StringParam:
			if len(param.Value) > maxBytes {
				newStringPayload := param.Value[:len(param.Value)-maxBytes]
				request.ModifyParams(ctx, newStringPayload)
			}
		}
		response, err = executeUnaryRequest(ctx, s.chain, request, s.upstreamSupervisor, upstreamStrategy)
		if err != nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
			}
		}
	} else if specs.IsStickyCreateMethod(request.SpecMethod()) {
		response, err = executeUnaryRequest(ctx, s.chain, request, s.upstreamSupervisor, upstreamStrategy)
		if err != nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
			}
		} else {
			if !response.Response.HasError() {
				bodyWithoutLastByte := response.Response.ResponseResult()[:len(response.Response.ResponseResult())-1]
				upstreamHash := []byte(s.upstreamSupervisor.GetUpstream(response.UpstreamId).GetHashIndex())
				body := append(append(bodyWithoutLastByte, upstreamHash...), []byte(`"`)...)
				response = &protocol.ResponseHolderWrapper{
					UpstreamId: response.UpstreamId,
					RequestId:  response.RequestId,
					Response:   protocol.NewSimpleHttpUpstreamResponse(response.Response.Id(), body, request.RequestType()),
				}
			}
		}
	} else {
		response = &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewTotalFailureFromErr(request.Id(), protocol.ServerError(), request.RequestType()),
		}
	}

	return &UnaryResponse{ResponseWrapper: response}
}

var _ RequestProcessor = (*StickyRequestProcessor)(nil)
