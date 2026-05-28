package connectors

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog"
	"golang.org/x/net/proxy"
)

type HttpConnector struct {
	endpoint          string
	httpClient        *http.Client
	additionalHeaders map[string]string
	connectorType     specs.ApiConnectorType
	torProxyUrl       string
}

func (h *HttpConnector) Unsubscribe(_ string) {
}

func NewHttpConnectorWithDefaultClient(
	connectorConfig *config.ApiConnectorConfig,
	connectorType specs.ApiConnectorType,
	torProxyUrl string,
) *HttpConnector {
	return &HttpConnector{
		endpoint:          connectorConfig.Url,
		httpClient:        http.DefaultClient,
		connectorType:     connectorType,
		additionalHeaders: connectorConfig.Headers,
		torProxyUrl:       torProxyUrl,
	}
}

func NewHttpConnector(
	connectorConfig *config.ApiConnectorConfig,
	connectorType specs.ApiConnectorType,
	torProxyUrl string,
) (*HttpConnector, error) {
	endpoint, err := url.Parse(connectorConfig.Url)
	if err != nil {
		return nil, fmt.Errorf("error parsing the endpoint: %v", err)
	}
	transport := utils.DefaultHttpTransport()
	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	customCA, err := utils.GetCustomCAPool(connectorConfig.Ca)
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(endpoint.Hostname(), ".onion") {
		if torProxyUrl == "" {
			return nil, errors.New("tor proxy url is required for onion endpoints")
		}
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		socksProxy, err := proxy.SOCKS5("tcp", torProxyUrl, nil, dialer)
		if err != nil {
			return nil, fmt.Errorf("error creating socks5 proxy: %v", err)
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksProxy.Dial(network, addr)
		}
	} else if customCA != nil {
		transport.TLSClientConfig = &tls.Config{
			RootCAs: customCA,
		}
	}
	client.Transport = transport

	return &HttpConnector{
		endpoint:          connectorConfig.Url,
		httpClient:        client,
		connectorType:     connectorType,
		additionalHeaders: connectorConfig.Headers,
		torProxyUrl:       torProxyUrl,
	}, nil
}

func (h *HttpConnector) Start() {
}

func (h *HttpConnector) Stop() {
}

func (h *HttpConnector) Running() bool {
	return true
}

func (h *HttpConnector) SubscribeStates(_ string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

// SendRequest dispatches into the JSON-RPC or REST flow based on the
// connector type. Each flow is responsible for building its own *http.Request
// and applying its own URL/header semantics; the shared dispatch helper
// only handles network execution and response framing.
func (h *HttpConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	if h.GetType() == specs.JsonRpcConnector {
		return h.sendJsonRpc(ctx, request)
	}
	return h.sendRest(ctx, request)
}

// sendJsonRpc forwards a JSON-RPC call. Always POST to the configured
// endpoint, body verbatim, no per-request path/header rewriting.
func (h *HttpConnector) sendJsonRpc(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	body, err := request.Body()
	if err != nil {
		return clientFailure(request, fmt.Errorf("error parsing a request body: %v", err))
	}

	quorumParams, quorumRequested := quorum.FromContext(ctx)
	endpoint := h.endpoint
	if quorumRequested {
		endpoint, err = appendQuery(endpoint, quorumParams.EncodeQuery())
		if err != nil {
			return clientFailure(request, fmt.Errorf("invalid upstream url %q: %w", h.endpoint, err))
		}
	}

	req, err := http.NewRequestWithContext(ctx, protocol.Post.String(), endpoint, bytes.NewReader(body))
	if err != nil {
		return clientFailure(request, fmt.Errorf("error creating an http request: %v", err))
	}
	h.applyConfigHeaders(req)

	// JSON-RPC streaming requires peeking the body to distinguish an error
	// envelope from a result value before we commit to streaming it.
	return h.dispatch(ctx, request, req, quorumRequested, jsonRpcCanStream)
}

// sendRest forwards a REST call. Expands the request's method template
// using its captured path params, layers on the client query and headers,
// and POSTs/GETs/... at the upstream as appropriate.
func (h *HttpConnector) sendRest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	restReq, ok := request.(*protocol.UpstreamRestRequest)
	if !ok {
		return clientFailure(request, errors.New("rest connector received a non-rest request"))
	}

	rp := restReq.RequestParams()
	var pathParams []string
	if rp != nil {
		pathParams = rp.PathParams
	}
	verb, path, err := utils.BuildRestURL(restReq.Method(), pathParams)
	if err != nil {
		return clientFailure(request, err)
	}
	if verb == "" || path == "" {
		return clientFailure(request, errors.New("no method and url path specified for an http request"))
	}

	target := joinEndpointAndPath(h.endpoint, path)
	if rp != nil && len(rp.QueryParams) > 0 {
		target, err = appendQuery(target, encodeMultiValuedQuery(rp.QueryParams))
		if err != nil {
			return clientFailure(request, fmt.Errorf("invalid upstream url %q: %w", target, err))
		}
	}
	quorumParams, quorumRequested := quorum.FromContext(ctx)
	if quorumRequested {
		target, err = appendQuery(target, quorumParams.EncodeQuery())
		if err != nil {
			return clientFailure(request, fmt.Errorf("invalid upstream url %q: %w", target, err))
		}
	}

	body, err := request.Body()
	if err != nil {
		return clientFailure(request, fmt.Errorf("error parsing a request body: %v", err))
	}

	req, err := http.NewRequestWithContext(ctx, verb, target, bytes.NewReader(body))
	if err != nil {
		return clientFailure(request, fmt.Errorf("error creating an http request: %v", err))
	}
	h.applyConfigHeaders(req)
	if rp != nil {
		h.applyClientHeaders(req, rp.Headers)
	}

	// REST bodies are opaque pass-through; if the caller asked for streaming
	// we hand them whatever the upstream gave us.
	return h.dispatch(ctx, request, req, quorumRequested, alwaysStream)
}

// applyConfigHeaders sets the connector's configured headers plus the
// JSON Content-Type default. Used by both flows.
func (h *HttpConnector) applyConfigHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.additionalHeaders {
		req.Header.Set(k, v)
	}
}

// applyClientHeaders forwards per-request client headers onto the upstream
// request, except keys the connector config already owns - those are
// typically auth tokens that a curious client must not be able to override.
func (h *HttpConnector) applyClientHeaders(req *http.Request, headers map[string][]string) {
	for k, vs := range headers {
		if _, taken := h.additionalHeaders[k]; taken {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

// canStreamFunc decides, after we've started reading the response body,
// whether it's safe to stream the rest to the client. REST bodies are
// always streamable; JSON-RPC needs a peek to rule out an error envelope.
type canStreamFunc func(*bufio.Reader) bool

func alwaysStream(_ *bufio.Reader) bool { return true }

func jsonRpcCanStream(r *bufio.Reader) bool {
	return protocol.ResponseCanBeStreamed(r, protocol.MaxChunkSize)
}

// dispatch executes the prepared *http.Request and frames the response as
// either a streaming or fully-buffered ResponseHolder. Quorum reads always
// buffer because the signature is computed over the full body.
func (h *HttpConnector) dispatch(
	ctx context.Context,
	request protocol.RequestHolder,
	req *http.Request,
	quorumRequested bool,
	allowStream canStreamFunc,
) protocol.ResponseHolder {
	resp, err := h.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return protocol.NewTotalFailure(request, protocol.CtxError(err))
		}
		return protocol.NewPartialFailure(
			request,
			protocol.ServerErrorWithCause(fmt.Errorf("unable to get an http response: %v", err)),
		)
	}

	if request.IsStream() && resp.StatusCode == 200 && !quorumRequested {
		bufReader := bufio.NewReaderSize(resp.Body, protocol.MaxChunkSize)
		if allowStream(bufReader) {
			zerolog.Ctx(ctx).Debug().Msgf("streaming response of method %s", request.Method())
			streamResp := protocol.NewHttpUpstreamResponseStream(request.Id(), protocol.NewCloseReader(ctx, bufReader, resp.Body), request.RequestType())
			return streamResp.WithResponseHeaders(resp.Header)
		}
		defer utils.CloseBodyReader(ctx, resp.Body)
		return h.receiveWholeResponse(ctx, request, resp.StatusCode, resp.Header, bufReader)
	}

	defer utils.CloseBodyReader(ctx, resp.Body)
	return h.receiveWholeResponse(ctx, request, resp.StatusCode, resp.Header, resp.Body)
}

func (h *HttpConnector) receiveWholeResponse(
	ctx context.Context,
	request protocol.RequestHolder,
	status int,
	headers http.Header,
	reader io.Reader,
) protocol.ResponseHolder {
	body, err := io.ReadAll(reader)
	if err != nil {
		if ctx.Err() != nil {
			return protocol.NewTotalFailure(request, protocol.CtxError(err))
		}
		return protocol.NewPartialFailure(
			request,
			protocol.ServerErrorWithCause(fmt.Errorf("unable to read an http response: %v", err)),
		)
	}
	return protocol.NewHttpUpstreamResponse(request.Id(), body, status, request.RequestType()).
		WithResponseHeaders(headers)
}

func (h *HttpConnector) GetType() specs.ApiConnectorType {
	return h.connectorType
}

func (h *HttpConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

// clientFailure is shorthand for the recurring "client error, total failure"
// branch the send paths use when they can't build a usable request.
func clientFailure(request protocol.RequestHolder, cause error) protocol.ResponseHolder {
	return protocol.NewTotalFailure(request, protocol.ClientError(cause))
}

// encodeMultiValuedQuery turns a multi-valued query map into "k1=v1&k1=v2&k2=v3".
// Free function so the send paths can use it without aliasing "net/url"
// around local variables named url/target.
func encodeMultiValuedQuery(params map[string][]string) string {
	if len(params) == 0 {
		return ""
	}
	values := make(url.Values, len(params))
	for k, vs := range params {
		values[k] = vs
	}
	return values.Encode()
}

// joinEndpointAndPath splices a literal request path into the connector's
// base URL while preserving any query the base already carries (e.g. an
// API key baked into the endpoint config):
//
//	"https://api.example.com/v1?key=x" + "/accounts/abc"
//	  -> "https://api.example.com/v1/accounts/abc?key=x"
//
// We split on "?" manually rather than going through url.Parse so the
// literal path components reach the upstream byte-for-byte - some upstreams
// compare the original path bytes in signature pre-images.
func joinEndpointAndPath(endpoint, path string) string {
	base, query, hasQuery := strings.Cut(endpoint, "?")
	full := base + path
	if hasQuery && query != "" {
		full += "?" + query
	}
	return full
}

// appendQuery merges an already-encoded query string into a URL, preserving
// any existing query/fragment/userinfo. `extraQuery` wins on duplicate keys.
func appendQuery(rawURL, extraQuery string) (string, error) {
	if extraQuery == "" {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, err
	}
	extra, err := url.ParseQuery(extraQuery)
	if err != nil {
		return rawURL, err
	}
	q := u.Query()
	for k, vs := range extra {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

var _ ApiConnector = (*HttpConnector)(nil)
