package flow

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
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
	if !specs.IsLocalMethod(chains.GetMethodSpecNameByChain(l.chain), request.Method()) {
		return &UnaryResponse{processedServerError(request, fmt.Errorf("method '%s' is not local", request.Method()))}
	}

	if l.subCtx != nil {
		body, _ := request.Body()
		node, err := sonic.Get(body, "params", 0)
		if err != nil {
			return &UnaryResponse{processedServerError(request, err)}
		}
		value, err := node.Raw()
		if err != nil {
			return &UnaryResponse{processedServerError(request, err)}
		}
		l.subCtx.Unsubscribe(protocol.ResultAsString([]byte(value)))
		return &UnaryResponse{
			&protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), ResultTrue, request.RequestType()),
			},
		}
	} else {
		chain := chains.GetChain(l.chain.String())
		var response protocol.ResponseHolder = protocol.NewTotalFailureFromErr(
			request.Id(),
			fmt.Errorf("there no local handler for  method '%s'", request.Method()),
			request.RequestType(),
		)
		switch request.Method() {
		case "eth_chainId":
			response = protocol.NewSimpleHttpUpstreamResponse(request.Id(), []byte(fmt.Sprintf(`"%s"`, chain.ChainId)), request.RequestType())
		case "net_version":
			response = protocol.NewSimpleHttpUpstreamResponse(request.Id(), []byte(fmt.Sprintf(`"%s"`, chain.NetVersion)), request.RequestType())
		}
		return &UnaryResponse{
			&protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   response,
			},
		}
	}
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
