package connectors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"io"
	"net/http"
	"strings"
)

type HttpConnector struct {
	endpoint          string
	httpClient        *http.Client
	additionalHeaders map[string]string
	connectorType     protocol.ApiConnectorType
}

func NewHttpConnector(endpoint string, connectorType protocol.ApiConnectorType, additionalHeaders map[string]string) *HttpConnector {
	return &HttpConnector{
		endpoint:          endpoint,
		httpClient:        http.DefaultClient,
		connectorType:     connectorType,
		additionalHeaders: additionalHeaders,
	}
}

func (h *HttpConnector) SendRequest(ctx context.Context, request protocol.UpstreamRequest) protocol.UpstreamResponse {
	url, httpMethod, err := h.requestParams(request)
	if err != nil {
		return protocol.NewHttpUpstreamResponseWithError(
			protocol.NewClientUpstreamError(err),
		)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, bytes.NewReader(request.Body()))
	if err != nil {
		return protocol.NewHttpUpstreamResponseWithError(
			protocol.NewClientUpstreamError(fmt.Errorf("error creating an http request: %v", err)),
		)
	}
	req.Header.Set("Content-Type", "application/json")
	for headerKey, headerValue := range h.additionalHeaders {
		req.Header.Set(headerKey, headerValue)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return protocol.NewHttpUpstreamResponseWithError(
			protocol.NewServerUpstreamError(fmt.Errorf("unable to get an http response: %v", err)),
		)
	}

	if request.IsStream() {
		return nil
	} else {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return protocol.NewHttpUpstreamResponseWithError(
				protocol.NewServerUpstreamError(fmt.Errorf("unable to read an http response: %v", err)),
			)
		}

		return protocol.NewHttpUpstreamResponse(request.Id(), body, resp.StatusCode, h.responseType())
	}
}

func (h *HttpConnector) GetType() protocol.ApiConnectorType {
	return h.connectorType
}

func (h *HttpConnector) Subscribe(_ context.Context, _ protocol.UpstreamRequest) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (h *HttpConnector) responseType() protocol.ResponseType {
	if h.GetType() == protocol.JsonRpcConnector {
		return protocol.JsonRpc
	} else {
		return protocol.Rest
	}
}

func (h *HttpConnector) requestParams(request protocol.UpstreamRequest) (string, string, error) {
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
