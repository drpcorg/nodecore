package connectors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/rs/zerolog"
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
		return protocol.NewTotalFailure(
			request,
			protocol.ClientError(err),
		)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, bytes.NewReader(request.Body()))
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
			zerolog.Ctx(ctx).Info().Msgf("streaming response of method %s", request.Method())
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
