package connectors

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/ws"
)

type WsConnector struct {
	connection *ws.WsConnection
}

var _ ApiConnector = (*WsConnector)(nil)

func NewWsConnector(connection *ws.WsConnection) *WsConnector {
	return &WsConnector{
		connection: connection,
	}
}

func (w *WsConnector) SendRequest(ctx context.Context, request protocol.UpstreamRequest) protocol.UpstreamResponse {
	return nil
}

func (w *WsConnector) Subscribe(ctx context.Context, request protocol.UpstreamRequest) (protocol.UpstreamSubscriptionResponse, error) {
	respChan, err := w.connection.SendWsRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	return protocol.NewJsonRpcWsUpstreamResponse(respChan), nil
}

func (w *WsConnector) GetType() protocol.ApiConnectorType {
	return protocol.WsConnector
}
