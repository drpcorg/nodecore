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
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog"
	"golang.org/x/net/proxy"
)

type HttpConnector struct {
	endpoint          string
	httpClient        *http.Client
	additionalHeaders map[string]string
	connectorType     protocol.ApiConnectorType
	torProxyUrl       string
}

func (h *HttpConnector) Unsubscribe(_ string) {
}

func NewHttpConnectorWithDefaultClient(
	connectorConfig *config.ApiConnectorConfig,
	connectorType protocol.ApiConnectorType,
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
	connectorType protocol.ApiConnectorType,
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

func (h *HttpConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	url, httpMethod, err := h.requestParams(request)
	if err != nil {
		return protocol.NewTotalFailure(
			request,
			protocol.ClientError(err),
		)
	}
	// Forward quorum params (quorum=N&quorum_required=n) to the upstream when the
	// client requested quorum reads. The upstream (e.g. drpc astream) responds
	// with QR<N>-id-<req-id> signature headers that we verify after receiving.
	// Quorum signatures are computed over the full response body, so streaming
	// is force-disabled below: we need the whole payload buffered to hash.
	quorumParams, quorumRequested := quorum.FromContext(ctx)
	if quorumRequested {
		url, err = appendQuery(url, quorumParams.EncodeQuery())
		if err != nil {
			return protocol.NewTotalFailure(
				request,
				protocol.ClientError(fmt.Errorf("invalid upstream url %q: %w", url, err)),
			)
		}
	}

	body, err := request.Body()
	if err != nil {
		return protocol.NewTotalFailure(
			request,
			protocol.ClientError(fmt.Errorf("error parsing a request body: %v", err)),
		)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, bytes.NewReader(body))
	if err != nil {
		return protocol.NewTotalFailure(
			request,
			protocol.ClientError(fmt.Errorf("error creating an http request: %v", err)),
		)
	}
	req.Header.Set("Content-Type", "application/json")
	for headerKey, headerValue := range h.additionalHeaders {
		req.Header.Set(headerKey, headerValue)
	}

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
		// if this is a REST request then it can be streamed as is
		// if this is a JSON-RPC request, first it's necessary to understand if there is an error or not
		canBeStreamed := request.RequestType() == protocol.Rest || protocol.ResponseCanBeStreamed(bufReader, protocol.MaxChunkSize)
		if canBeStreamed {
			zerolog.Ctx(ctx).Debug().Msgf("streaming response of method %s", request.Method())
			streamResp := protocol.NewHttpUpstreamResponseStream(request.Id(), protocol.NewCloseReader(ctx, bufReader, resp.Body), request.RequestType())
			return streamResp.WithResponseHeaders(resp.Header)
		} else {
			defer utils.CloseBodyReader(ctx, resp.Body)
			return h.receiveWholeResponse(ctx, request, resp.StatusCode, resp.Header, bufReader)
		}
	} else {
		defer utils.CloseBodyReader(ctx, resp.Body)
		return h.receiveWholeResponse(ctx, request, resp.StatusCode, resp.Header, resp.Body)
	}
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

func (h *HttpConnector) GetType() protocol.ApiConnectorType {
	return h.connectorType
}

func (h *HttpConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
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

func (h *HttpConnector) requestParams(request protocol.RequestHolder) (string, string, error) {
	if h.GetType() == protocol.JsonRpcConnector {
		return h.endpoint, protocol.Post.String(), nil
	}
	requestParams := strings.Split(request.Method(), protocol.MethodSeparator)
	if len(requestParams) == 0 || !strings.Contains(request.Method(), protocol.MethodSeparator) {
		return "", "", errors.New("no method and url path specified for an http request")
	}
	httpMethod := requestParams[0]
	endpointParams := strings.Split(h.endpoint, "?")

	url := endpointParams[0] + requestParams[1]
	if len(endpointParams) == 2 {
		queryParams := endpointParams[1]
		if strings.Contains(requestParams[1], "?") {
			url = fmt.Sprintf("%s&%s", url, queryParams)
		} else {
			url = fmt.Sprintf("%s?%s", url, queryParams)
		}
	}

	return url, httpMethod, nil
}

var _ ApiConnector = (*HttpConnector)(nil)
