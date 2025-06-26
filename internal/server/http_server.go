package server

import (
	"context"
	"github.com/bytedance/sonic/decoder"
	"github.com/bytedance/sonic/encoder"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/rating"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/flow"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"io"
	"net"
	"net/http"
	"strings"
)

type Request struct {
	Chain            string
	UpstreamRequests []protocol.RequestHolder
}

type Response struct {
	ResponseReader io.Reader
	Order          int
}

type ApplicationContext struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
	cacheProcessor     caches.CacheProcessor
	registry           *rating.RatingRegistry
}

func NewApplicationContext(upstreamSupervisor upstreams.UpstreamSupervisor, cacheProcessor caches.CacheProcessor, registry *rating.RatingRegistry) *ApplicationContext {
	return &ApplicationContext{
		upstreamSupervisor: upstreamSupervisor,
		cacheProcessor:     cacheProcessor,
		registry:           registry,
	}
}

type FastJSONSerializer struct{}

func (FastJSONSerializer) Serialize(c echo.Context, i interface{}, indent string) error {
	enc := encoder.NewStreamEncoder(c.Response())
	if indent != "" {
		enc.SetIndent("", indent)
	}
	return enc.Encode(i)
}

func (FastJSONSerializer) Deserialize(c echo.Context, i interface{}) error {
	return decoder.NewStreamDecoder(c.Request().Body).Decode(i)
}

func NewHttpServer(ctx context.Context, appCtx *ApplicationContext) *echo.Echo {
	httpServer := echo.New()
	httpServer.Server.BaseContext = func(listener net.Listener) context.Context {
		return ctx
	}
	httpServer.JSONSerializer = &FastJSONSerializer{}
	httpServer.Use(middleware.Decompress())

	httpGroup := httpServer.Group("/queries/:chain")
	httpGroup.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Header.Get("Upgrade") == "websocket" {
				handleWebsocket(c, appCtx)
				return nil
			}
			return handleHttp(c, appCtx)
		}
	})

	return httpServer
}

func handleHttp(reqCtx echo.Context, appCtx *ApplicationContext) error {
	chain := reqCtx.Param("chain")
	httpRequest := reqCtx.Request()
	ctx := httpRequest.Context()
	path := strings.Split(httpRequest.URL.Path, chain)

	preRequest := &Request{
		Chain: chain,
	}
	var requestHandler RequestHandler
	var err error
	var reqType protocol.RequestType
	if httpRequest.Method == "POST" && len(path) == 2 && path[1] == "" {
		requestHandler, err = NewJsonRpcHandler(preRequest, httpRequest.Body, false)
		reqType = protocol.JsonRpc
	} else {
		requestHandler, err = NewRestHandler(preRequest, "", httpRequest.Body)
		reqType = protocol.Rest
	}

	if err != nil {
		resp := protocol.NewTotalFailureFromErr("0", protocol.ParseError(), reqType)
		return writeResponse(
			reqCtx.Response(),
			protocol.ToHttpCode(resp),
			resp.EncodeResponse([]byte("0")),
		)
	}
	responseWrappers := handleRequest(ctx, requestHandler, appCtx, nil)

	return handleResponse(ctx, requestHandler, reqCtx.Response(), responseWrappers)
}

func handleResponse(
	ctx context.Context,
	requestHandler RequestHandler,
	httpResponse *echo.Response,
	responseWrappers chan *protocol.ResponseHolderWrapper,
) error {
	var responseReader io.Reader
	code := http.StatusOK
	if !requestHandler.IsSingle() {
		responses := utils.Map(responseWrappers, func(wrapper *protocol.ResponseHolderWrapper) *Response {
			return requestHandler.ResponseEncode(wrapper.Response)
		})
		responseReader = ArraySortingStream(ctx, responses, requestHandler.RequestCount())
	} else {
		select {
		case <-ctx.Done():
			resp := protocol.NewTotalFailureFromErr("0", protocol.RequestTimeoutError(), requestHandler.GetRequestType())
			return writeResponse(
				httpResponse,
				protocol.ToHttpCode(resp),
				resp.EncodeResponse([]byte("0")),
			)
		case responseWrapper, ok := <-responseWrappers:
			if ok {
				code = protocol.ToHttpCode(responseWrapper.Response)
				responseReader = requestHandler.ResponseEncode(responseWrapper.Response).ResponseReader
			}
		}
	}
	return writeResponse(httpResponse, code, responseReader)
}

func writeResponse(httpResponse *echo.Response, code int, responseReader io.Reader) error {
	httpResponse.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	httpResponse.WriteHeader(code)
	_, err := io.Copy(httpResponse, responseReader)
	return err
}

func handleRequest(ctx context.Context, requestHandler RequestHandler, appCtx *ApplicationContext, subCtx *flow.SubCtx) chan *protocol.ResponseHolderWrapper {
	request, err := requestHandler.RequestDecode(ctx)
	if err != nil {
		return createWrapperFromError(request, err, requestHandler.GetRequestType())
	}
	if !chains.IsSupported(request.Chain) {
		return createWrapperFromError(request, protocol.WrongChainError(request.Chain), requestHandler.GetRequestType())
	}
	chain := chains.GetChain(request.Chain).Chain

	if appCtx.upstreamSupervisor.GetChainSupervisor(chain) == nil {
		return createWrapperFromError(request, protocol.NoAvailableUpstreamsError(), requestHandler.GetRequestType())
	}

	executionFlow := flow.NewBaseExecutionFlow(chain, appCtx.upstreamSupervisor, appCtx.cacheProcessor, appCtx.registry, subCtx)
	go executionFlow.Execute(ctx, request.UpstreamRequests)
	responseChan := executionFlow.GetResponses()

	return responseChan
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
