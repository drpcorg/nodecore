package server

import (
	"context"
	"io"
	"net"
	"net/http"
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

	requestHandler := func(c echo.Context) error {
		if c.Request().Method == http.MethodOptions {
			return handleCorsOptions(c)
		}
		start := time.Now()
		c.Request().SetPathValue("key", c.Param("key"))
		chain := c.Param("chain")
		restPath := c.Param("*") // for rest requests
		reqCtx := utils.ContextWithIps(c.Request().Context(), c.Request())
		reqType := lo.Ternary(len(restPath) > 0, protocol.Rest, protocol.JsonRpc)
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

		if c.Request().Header.Get("Upgrade") == "websocket" {
			handleWebsocket(reqCtx, c, chain, authPayload, appCtx)
			return nil
		}
		err = handleHttp(reqCtx, c, chain, reqType, authPayload, appCtx)
		requestTimeToLastByte.Observe(time.Since(start).Seconds())
		return err
	}

	httpGroup.Any("/api-key/:key/*", requestHandler)
	httpGroup.Any("/api-key/:key", requestHandler)
	httpGroup.Any("/*", requestHandler)
	httpGroup.Any("", requestHandler)

	return httpServer
}

var corsHeaders = []lo.Tuple2[string, string]{
	lo.T2("Origin", "Access-Control-Allow-Origin"),
	lo.T2("Access-Control-Request-Headers", "Access-Control-Allow-Headers"),
	lo.T2("Access-Control-Request-Method", "Access-Control-Allow-Methods"),
}

func handleCorsOptions(c echo.Context) error {
	for _, header := range corsHeaders {
		if requestHeaderValue := c.Request().Header.Get(header.A); requestHeaderValue != "" {
			c.Response().Header().Set(header.B, requestHeaderValue)
		}
	}
	return c.NoContent(http.StatusNoContent)
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
	handleResp := handleRequest(ctx, requestHandler, authPayload, appCtx, nil)

	return handleResponse(ctx, requestHandler, reqCtx, handleResp)
}

func handleResponse(
	ctx context.Context,
	requestHandler RequestHandler,
	reqCtx echo.Context,
	handleResp *HandleResponse,
) error {
	var responseReader io.Reader
	code := http.StatusOK
	httpResponse := reqCtx.Response()
	if !requestHandler.IsSingle() {
		responses := utils.Map(handleResp.responseWrappers, func(wrapper *protocol.ResponseHolderWrapper) *Response {
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
		case responseWrapper, ok := <-handleResp.responseWrappers:
			if ok {
				httpResponse.Header().Set("response-provider", responseWrapper.UpstreamId)
				code = protocol.ToHttpCode(responseWrapper.Response)
				responseReader = requestHandler.ResponseEncode(responseWrapper.Response).ResponseReader
			}
		}
	}

	setCorsHeaders(reqCtx, handleResp.corsOrigins)

	return writeResponse(httpResponse, code, responseReader)
}

func setCorsHeaders(reqCtx echo.Context, corsOrigins []string) {
	if len(corsOrigins) > 0 {
		origin := reqCtx.Request().Header.Get("Origin")
		for _, item := range corsOrigins {
			if utils.MatchWildcards(item, origin) {
				reqCtx.Response().Header().Set("Access-Control-Allow-Origin", origin)
				reqCtx.Response().Header().Set("Vary", "Origin")
				return
			}
		}
	} else {
		reqCtx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	}
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
) *HandleResponse {
	var request *Request

	corsOrigins, err := appCtx.authProcessor.PreKeyValidate(ctx, authPayload)
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

	if appCtx.upstreamSupervisor.GetChainSupervisor(chain) == nil {
		return NewHandleResponse(
			createWrapperFromError(request, protocol.NoAvailableUpstreamsError(), requestHandler.GetRequestType()),
			nil,
		)
	}

	for _, requestHolder := range request.UpstreamRequests {
		err = appCtx.authProcessor.PostKeyValidate(ctx, authPayload, requestHolder)
		if err != nil {
			return NewHandleResponse(
				createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType()),
				nil,
			)
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
