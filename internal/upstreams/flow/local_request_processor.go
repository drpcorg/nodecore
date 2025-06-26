package flow

import (
	"context"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
)

type LocalRequestProcessor struct {
	chain  chains.Chain
	subCtx *SubCtx
}

var ResultTrue = []byte(`true`)

func (l *LocalRequestProcessor) ProcessRequest(
	_ context.Context,
	_ UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	responseWrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), ResultTrue, request.RequestType()),
	}

	if l.subCtx != nil && specs.IsUnsubscribeMethod(chains.GetMethodSpecNameByChain(l.chain), request.Method()) {
		node, err := sonic.Get(request.Body(), "params", 0)
		if err != nil {
			return &UnaryResponse{processedServerError(request, err)}
		}
		value, err := node.Raw()
		if err != nil {
			return &UnaryResponse{processedServerError(request, err)}
		}
		l.subCtx.Unsubscribe(protocol.ResultAsString([]byte(value)))
	}

	return &UnaryResponse{responseWrapper}
}

func NewLocalRequestProcessor(chain chains.Chain, subCtx *SubCtx) *LocalRequestProcessor {
	return &LocalRequestProcessor{chain: chain, subCtx: subCtx}
}

var _ RequestProcessor = (*LocalRequestProcessor)(nil)

func processedServerError(request protocol.RequestHolder, cause error) *protocol.ResponseHolderWrapper {
	serverErr := protocol.ServerErrorWithCause(cause)
	return &protocol.ResponseHolderWrapper{
		UpstreamId: NoUpstream,
		RequestId:  request.Id(),
		Response:   protocol.NewTotalFailureFromErr(request.Id(), serverErr, request.RequestType()),
	}
}
