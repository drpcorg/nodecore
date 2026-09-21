package server_ctx

import (
	"context"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats/hook"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// RequestHandler decodes one ingress request into upstream request holders.
// It is the minimal contract HandleRequest needs; protocol-specific handlers
// (HTTP JSON-RPC/REST, WS, gRPC) may expose more methods on top for their own
// response encoding.
type RequestHandler interface {
	RequestDecode(context.Context) (*Request, error)
	GetRequestType() protocol.RequestType
}

type Request struct {
	Chain            string
	UpstreamRequests []protocol.RequestHolder
}

type HandleResponse struct {
	responseWrappers chan *protocol.ResponseHolderWrapper
	corsOrigins      []string
}

func NewHandleResponse(responseWrappers chan *protocol.ResponseHolderWrapper, corsOrigins []string) *HandleResponse {
	return &HandleResponse{
		responseWrappers: responseWrappers,
		corsOrigins:      corsOrigins,
	}
}

func (h *HandleResponse) ResponseWrappers() chan *protocol.ResponseHolderWrapper {
	return h.responseWrappers
}

func (h *HandleResponse) CorsOrigins() []string {
	return h.corsOrigins
}

// HandleRequest is the shared ingress pipeline every client-facing server
// (HTTP, WS, gRPC) routes through: key pre-validation, request decoding,
// chain resolution, per-request key validation, and the execution flow with
// the canonical hook set. Errors come back as response wrappers on the
// channel - each ingress renders them in its own protocol shape.
func (a *ApplicationServerContext) HandleRequest(
	ctx context.Context,
	requestHandler RequestHandler,
	authPayload auth.AuthPayload,
	subCtx *flow.SubCtx,
) *HandleResponse {
	var request *Request

	corsOrigins, err := a.AuthProcessor.PreKeyValidate(ctx, authPayload)
	if err != nil {
		return NewHandleResponse(
			createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType()),
			nil,
		)
	}

	request, err = requestHandler.RequestDecode(ctx)
	if err != nil {
		return NewHandleResponse(createWrapperFromError(request, err, requestHandler.GetRequestType()), nil)
	}
	if !chains.IsSupported(request.Chain) {
		return NewHandleResponse(
			createWrapperFromError(request, protocol.WrongChainError(request.Chain), requestHandler.GetRequestType()),
			nil,
		)
	}
	chain := chains.GetChain(request.Chain).Chain

	if a.UpstreamSupervisor.GetChainSupervisor(chain) == nil {
		return NewHandleResponse(
			createWrapperFromError(request, protocol.NoAvailableUpstreamsError(), requestHandler.GetRequestType()),
			nil,
		)
	}

	for _, requestHolder := range request.UpstreamRequests {
		err = a.AuthProcessor.PostKeyValidate(ctx, authPayload, requestHolder)
		if err != nil {
			return NewHandleResponse(
				createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType()),
				nil,
			)
		}
		requestHolder.RequestObserver().
			WithApiKey(a.AuthProcessor.GetKeyValue(authPayload))
	}

	executionFlow := flow.NewGenericExecutionFlow(
		chain,
		a.UpstreamSupervisor,
		a.CacheProcessor,
		a.Registry,
		a.AppConfig,
		subCtx,
		a.QuorumRegistry,
		a.SubEngineRegistry,
	)
	executionFlow.AddHooks(
		flow.NewMethodBanHook(a.UpstreamSupervisor),
		dimensions.NewDimensionHook(a.DimensionTracker),
		hook.NewStatsHook(a.StatsService),
	)

	go executionFlow.Execute(ctx, request.UpstreamRequests)
	responseChan := executionFlow.GetResponses()

	return NewHandleResponse(responseChan, corsOrigins)
}

func createWrapperFromError(request *Request, err error, requestType protocol.RequestType) chan *protocol.ResponseHolderWrapper {
	respChan := make(chan *protocol.ResponseHolderWrapper)
	errWrapper := func(id string) *protocol.ResponseHolderWrapper {
		return &protocol.ResponseHolderWrapper{
			UpstreamId: flow.NoUpstream,
			RequestId:  id,
			Response:   protocol.NewTotalFailureFromErr(id, err, requestType),
		}
	}
	go func() {
		if request == nil || len(request.UpstreamRequests) == 0 {
			respChan <- errWrapper("0")
		} else {
			for _, req := range request.UpstreamRequests {
				respChan <- errWrapper(req.Id())
			}
		}
		close(respChan)
	}()
	return respChan
}
