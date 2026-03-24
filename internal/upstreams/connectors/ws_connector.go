package connectors

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type WsConnector struct {
	wsProcessor ws.WsProcessor
}

func NewWsConnector(connection ws.WsProcessor) *WsConnector {
	return &WsConnector{
		wsProcessor: connection,
	}
}

func (w *WsConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	wsResponse, err := w.wsProcessor.SendRpcRequest(ctx, request)
	if err != nil {
		// ws rpc requests won't be retried
		return protocol.NewTotalFailure(
			request,
			protocol.ServerErrorWithCause(fmt.Errorf("unable to get a response via ws - %v", err)),
		)
	}
	return protocol.NewWsJsonRpcResponse(request.Id(), wsResponse.Message, wsResponse.Error)
}

func (w *WsConnector) Subscribe(ctx context.Context, request protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	respChan, err := w.wsProcessor.SendWsRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	return protocol.NewJsonRpcWsUpstreamResponse(respChan), nil
}

func (w *WsConnector) GetType() protocol.ApiConnectorType {
	return protocol.WsConnector
}

func (w *WsConnector) SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return w.wsProcessor.SubscribeWsStates(name)
}

func (w *WsConnector) Start() {
	w.wsProcessor.Start()
}

func (w *WsConnector) Stop() {
	w.wsProcessor.Stop()
}

func (w *WsConnector) Running() bool {
	return w.wsProcessor.Running()
}

var _ ApiConnector = (*WsConnector)(nil)
