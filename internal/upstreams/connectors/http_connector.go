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

var _ ApiConnector = (*HttpConnector)(nil)

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
	transport := defaultHttpTransport()
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

func (h *HttpConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	url, httpMethod, err := h.requestParams(request)
	if err != nil {
		return protocol.NewTotalFailure(
			request,
			protocol.ClientError(err),
		)
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

	if request.IsStream() && resp.StatusCode == 200 {
		bufReader := bufio.NewReaderSize(resp.Body, protocol.MaxChunkSize)
		// if this is a REST request then it can be streamed as is
		// if this is a JSON-RPC request, first it's necessary to understand if there is an error or not
		canBeStreamed := request.RequestType() == protocol.Rest || protocol.ResponseCanBeStreamed(bufReader, protocol.MaxChunkSize)
		if canBeStreamed {
			zerolog.Ctx(ctx).Debug().Msgf("streaming response of method %s", request.Method())
			return protocol.NewHttpUpstreamResponseStream(request.Id(), protocol.NewCloseReader(ctx, bufReader, resp.Body), request.RequestType())
		} else {
			defer closeBodyReader(ctx, resp.Body)
			return h.receiveWholeResponse(ctx, request, resp.StatusCode, bufReader)
		}
	} else {
		defer closeBodyReader(ctx, resp.Body)
		return h.receiveWholeResponse(ctx, request, resp.StatusCode, resp.Body)
	}
}

func closeBodyReader(ctx context.Context, bodyReader io.ReadCloser) {
	err := bodyReader.Close()
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("couldn't close a body reader")
	}
}

func (h *HttpConnector) receiveWholeResponse(ctx context.Context, request protocol.RequestHolder, status int, reader io.Reader) protocol.ResponseHolder {
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

	return protocol.NewHttpUpstreamResponse(request.Id(), body, status, request.RequestType())
}

func (h *HttpConnector) GetType() protocol.ApiConnectorType {
	return h.connectorType
}

func (h *HttpConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func defaultHttpTransport() *http.Transport {
	// to move all these params to the config per upstream?
	return &http.Transport{
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   256,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	}
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
