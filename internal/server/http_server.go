package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic/decoder"
	"github.com/bytedance/sonic/encoder"
	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/klauspost/compress/gzip"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"
)

var requestTimeToLastByte = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: config.AppName,
		Subsystem: "http",
		Name:      "time_to_last_byte",
		Help:      "The histogram of HTTP request duration until the last byte is sent",
	},
)

func init() {
	prometheus.MustRegister(requestTimeToLastByte)
}

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
	authProcessor      auth.AuthProcessor
	appConfig          *config.AppConfig
	storageRegistry    *storages.StorageRegistry
}

func NewApplicationContext(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheProcessor caches.CacheProcessor,
	registry *rating.RatingRegistry,
	authProcessor auth.AuthProcessor,
	appConfig *config.AppConfig,
	storageRegistry *storages.StorageRegistry,
) *ApplicationContext {
	return &ApplicationContext{
		upstreamSupervisor: upstreamSupervisor,
		cacheProcessor:     cacheProcessor,
		registry:           registry,
		authProcessor:      authProcessor,
		appConfig:          appConfig,
		storageRegistry:    storageRegistry,
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

func configureServer(ctx context.Context, server *http.Server) {
	server.BaseContext = func(listener net.Listener) context.Context {
		return ctx
	}
	server.IdleTimeout = 1 * time.Minute // TODO: pass it to the config
	server.ReadTimeout = 1 * time.Minute
	server.WriteTimeout = 2 * time.Minute
}

func NewHttpServer(ctx context.Context, appCtx *ApplicationContext) *echo.Echo {
	httpServer := echo.New()
	httpServer.HideBanner = true
	configureServer(ctx, httpServer.Server)
	configureServer(ctx, httpServer.TLSServer)
	httpServer.JSONSerializer = &FastJSONSerializer{}
	httpServer.Use(middleware.Decompress())
	httpServer.Use(GzipWithConfig(GzipConfig{Level: gzip.BestSpeed}))

	httpGroup := httpServer.Group("/queries/:chain")
	httpGroup.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			reqCtx := utils.ContextWithIps(c.Request().Context(), c.Request())
			chain := c.Param("chain")
			path := strings.Split(c.Request().URL.Path, chain)
			isWs := c.Request().Header.Get("Upgrade") == "websocket"
			reqType := lo.Ternary(
				isWs || c.Request().Method == "POST" && len(path) == 2 && path[1] == "",
				protocol.JsonRpc,
				protocol.Rest,
			)
			authPayload := auth.NewHttpAuthPayload(c.Request())

			err := appCtx.authProcessor.Authenticate(c.Request().Context(), authPayload)
			if err != nil {
				resp := protocol.NewTotalFailureFromErr("0", protocol.AuthError(err), reqType)
				return writeResponse(
					c.Response(),
					protocol.ToHttpCode(resp),
					resp.EncodeResponse([]byte("0")),
				)
			}

			if isWs {
				handleWebsocket(reqCtx, c, chain, authPayload, appCtx)
				return nil
			}
			err = handleHttp(reqCtx, c, chain, reqType, authPayload, appCtx)
			requestTimeToLastByte.Observe(time.Since(start).Seconds())
			return err
		}
	})

	return httpServer
}

func handleHttp(
	ctx context.Context,
	reqCtx echo.Context,
	chain string,
	reqType protocol.RequestType,
	authPayload auth.AuthPayload,
	appCtx *ApplicationContext,
) error {
	preRequest := &Request{
		Chain: chain,
	}
	var requestHandler RequestHandler
	var err error
	if reqType == protocol.JsonRpc {
		requestHandler, err = NewJsonRpcHandler(preRequest, reqCtx.Request().Body, false)
	} else {
		requestHandler, err = NewRestHandler(preRequest, "", reqCtx.Request().Body)
	}

	if err != nil {
		resp := protocol.NewTotalFailureFromErr("0", protocol.ParseError(), reqType)
		return writeResponse(
			reqCtx.Response(),
			protocol.ToHttpCode(resp),
			resp.EncodeResponse([]byte("0")),
		)
	}
	responseWrappers := handleRequest(ctx, requestHandler, authPayload, appCtx, nil)

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
				httpResponse.Header().Set("response-provider", responseWrapper.UpstreamId)
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

func handleRequest(
	ctx context.Context,
	requestHandler RequestHandler,
	authPayload auth.AuthPayload,
	appCtx *ApplicationContext,
	subCtx *flow.SubCtx,
) chan *protocol.ResponseHolderWrapper {
	var request *Request

	err := appCtx.authProcessor.PreKeyValidate(ctx, authPayload)
	if err != nil {
		return createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType())
	}

	request, err = requestHandler.RequestDecode(ctx)
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

	for _, requestHolder := range request.UpstreamRequests {
		err = appCtx.authProcessor.PostKeyValidate(ctx, authPayload, requestHolder)
		if err != nil {
			return createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType())
		}
	}

	executionFlow := flow.NewBaseExecutionFlow(
		chain,
		appCtx.upstreamSupervisor,
		appCtx.cacheProcessor,
		appCtx.registry,
		appCtx.appConfig,
		subCtx,
	)
	executionFlow.AddHooks(
		flow.NewMethodBanHook(appCtx.upstreamSupervisor),
	)

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
