package connectors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/rs/zerolog/log"
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

var _ ApiConnector = (*HttpConnector)(nil)

func NewHttpConnector(endpoint string, connectorType protocol.ApiConnectorType, additionalHeaders map[string]string) *HttpConnector {
	return &HttpConnector{
		endpoint:          endpoint,
		httpClient:        http.DefaultClient,
		connectorType:     connectorType,
		additionalHeaders: additionalHeaders,
	}
}

func (h *HttpConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	url, httpMethod, err := h.requestParams(request)
	if err != nil {
		return h.createReplyError(
			request,
			protocol.ClientError(err),
		)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, bytes.NewReader(request.Body()))
	if err != nil {
		return h.createReplyError(
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
		return h.createReplyError(
			request,
			protocol.ServerError(fmt.Errorf("unable to get an http response: %v", err)),
		)
	}

	if request.IsStream() {
		return nil
	} else {
		defer func() {
			err = resp.Body.Close()
			if err != nil {
				log.Warn().Err(err).Msg("couldn't close a response body")
			}
		}()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return h.createReplyError(
				request,
				protocol.ServerError(fmt.Errorf("unable to read an http response: %v", err)),
			)
		}

		return protocol.NewHttpUpstreamResponse(request.Id(), body, resp.StatusCode, request.RequestType())
	}
}

func (h *HttpConnector) createReplyError(request protocol.RequestHolder, responseError *protocol.ResponseError) *protocol.ReplyError {
	return protocol.NewReplyError(
		request.Id(),
		responseError,
		request.RequestType(),
	)
}

func (h *HttpConnector) GetType() protocol.ApiConnectorType {
	return h.connectorType
}

func (h *HttpConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
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
