package http_server

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/stats/hook"

	"github.com/bytedance/sonic/decoder"
	"github.com/bytedance/sonic/encoder"
	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/klauspost/compress/gzip"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
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
	Suppress       bool
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

func NewHttpServer(ctx context.Context, appCtx *server_ctx.ApplicationServerContext) *echo.Echo {
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
		reqCtx = quorum.WithParams(reqCtx, quorum.ParamsFromQuery(c.Request().URL.Query()))
		reqType := lo.Ternary(len(restPath) > 0, protocol.Rest, protocol.JsonRpc)
		authPayload := auth.NewHttpAuthPayload(c.Request())

		err := appCtx.AuthProcessor.Authenticate(c.Request().Context(), authPayload)
		if err != nil {
			resp := protocol.NewTotalFailureFromErr("0", protocol.AuthError(err), reqType)
			return writeResponse(
				c.Response(),
				protocol.ToHttpCode(resp),
				resp.EncodeResponse([]byte("0")),
			)
		}

		if c.Request().Header.Get("Upgrade") == "websocket" {
			conn, err := upgrader.Upgrade(c.Response().Writer, c.Request(), nil)
			if err != nil {
				log.Error().Err(err).Msg("couldn't upgrade http to ws")
				return err
			}
			HandleWebsocket(reqCtx, conn, chain, authPayload, appCtx)
			return nil
		}
		err = handleHttp(reqCtx, c, chain, restPath, reqType, authPayload, appCtx)
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
	restPath string,
	reqType protocol.RequestType,
	authPayload auth.AuthPayload,
	appCtx *server_ctx.ApplicationServerContext,
) error {
	preRequest := &Request{
		Chain: chain,
	}
	var requestHandler RequestHandler
	var err error
	if reqType == protocol.JsonRpc {
		requestHandler, err = NewJsonRpcHandler(preRequest, reqCtx.Request().Body, false)
	} else {
		requestHandler, err = NewRestHandler(
			preRequest,
			reqCtx.Request(),
			restPath,
		)
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
		expected := requestHandler.RequestCount()
		if expected == 0 {
			for range handleResp.responseWrappers {
			}
			code = http.StatusNoContent
		} else {
			responses := make(chan *Response)
			go func() {
				defer close(responses)
				for wrapper := range handleResp.responseWrappers {
					response := requestHandler.ResponseEncode(wrapper.Response)
					if !response.Suppress {
						responses <- response
					}
				}
			}()
			responseReader = ArraySortingStream(ctx, responses, expected)
		}
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
				response := requestHandler.ResponseEncode(responseWrapper.Response)
				if response.Suppress {
					code = http.StatusNoContent
				} else {
					httpResponse.Header().Set("response-provider", responseWrapper.UpstreamId)
					code = protocol.ToHttpCode(responseWrapper.Response)
					responseReader = response.ResponseReader

					copyUpstreamResponseHeaders(httpResponse.Header(), responseWrapper.Response)
				}
			}
		}
	}

	setCorsHeaders(reqCtx, handleResp.corsOrigins)

	return writeResponse(httpResponse, code, responseReader)
}

// copyUpstreamResponseHeaders forwards an upstream's response headers onto
// the outgoing HTTP response. Used so REST clients see the upstream's
// Content-Type, quorum signature headers, CORS hints, etc. verbatim.
// Responses that don't implement HasResponseHeaders (e.g. ReplyError)
// silently contribute nothing.
func copyUpstreamResponseHeaders(dst http.Header, response protocol.ResponseHolder) {
	carrier, ok := response.(protocol.HasResponseHeaders)
	if !ok {
		return
	}
	for key, values := range carrier.ResponseHeaders() {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
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
	if responseReader == nil {
		httpResponse.WriteHeader(code)
		return nil
	}
	if httpResponse.Header().Get(echo.HeaderContentType) == "" {
		httpResponse.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	httpResponse.WriteHeader(code)
	_, err := io.Copy(httpResponse, responseReader)
	return err
}

func handleRequest(
	ctx context.Context,
	requestHandler RequestHandler,
	authPayload auth.AuthPayload,
	appCtx *server_ctx.ApplicationServerContext,
	subCtx *flow.SubCtx,
) *HandleResponse {
	var request *Request

	corsOrigins, err := appCtx.AuthProcessor.PreKeyValidate(ctx, authPayload)
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

	if appCtx.UpstreamSupervisor.GetChainSupervisor(chain) == nil {
		return NewHandleResponse(
			createWrapperFromError(request, protocol.NoAvailableUpstreamsError(), requestHandler.GetRequestType()),
			nil,
		)
	}

	for _, requestHolder := range request.UpstreamRequests {
		err = appCtx.AuthProcessor.PostKeyValidate(ctx, authPayload, requestHolder)
		if err != nil {
			return NewHandleResponse(
				createWrapperFromError(request, protocol.AuthError(err), requestHandler.GetRequestType()),
				nil,
			)
		}
		requestHolder.RequestObserver().
			WithApiKey(appCtx.AuthProcessor.GetKeyValue(authPayload))
	}

	executionFlow := flow.NewBaseExecutionFlow(
		chain,
		appCtx.UpstreamSupervisor,
		appCtx.CacheProcessor,
		appCtx.Registry,
		appCtx.AppConfig,
		subCtx,
		appCtx.QuorumRegistry,
		appCtx.SubEngineRegistry,
	)
	executionFlow.AddHooks(
		flow.NewMethodBanHook(appCtx.UpstreamSupervisor),
		dimensions.NewDimensionHook(appCtx.DimensionTracker),
		hook.NewStatsHook(appCtx.StatsService),
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
