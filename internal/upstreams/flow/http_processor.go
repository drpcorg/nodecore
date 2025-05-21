package flow

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
)

type UpstreamRequestProcessor interface {
	Execute(context.Context, protocol.RequestHolder) *protocol.ResponseHolderWrapper
}

type HttpUpstreamRequestProcessor struct {
	upstream      *upstreams.Upstream
	httpConnector connectors.ApiConnector
}

func (h *HttpUpstreamRequestProcessor) Execute(ctx context.Context, request protocol.RequestHolder) *protocol.ResponseHolderWrapper {
	response := h.httpConnector.SendRequest(ctx, request)

	return &protocol.ResponseHolderWrapper{
		RequestId:  request.Id(),
		UpstreamId: h.upstream.Id,
		Response:   response,
	}
}

func NewHttpUpstreamRequestProcessor(upstream *upstreams.Upstream, connectorType protocol.ApiConnectorType) (*HttpUpstreamRequestProcessor, error) {
	httpConnector := upstream.GetConnector(connectorType)
	if httpConnector == nil {
		return nil, fmt.Errorf("upstream %s doesn't have a %s connector", upstream.Id, connectorType)
	}

	return &HttpUpstreamRequestProcessor{
		httpConnector: httpConnector,
		upstream:      upstream,
	}, nil
}

var _ UpstreamRequestProcessor = (*HttpUpstreamRequestProcessor)(nil)
