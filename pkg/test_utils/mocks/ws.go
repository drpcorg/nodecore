package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type WsProcessorMock struct {
	mock.Mock
}

func NewWsProcessorMock() *WsProcessorMock {
	return &WsProcessorMock{}
}

func (w *WsProcessorMock) Start() {
	w.Called()
}

func (w *WsProcessorMock) Stop() {
	w.Called()
}

func (w *WsProcessorMock) Running() bool {
	args := w.Called()
	return args.Get(0).(bool)
}

func (w *WsProcessorMock) SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error) {
	args := w.Called(ctx, upstreamRequest)
	var resp *protocol.WsResponse
	if args.Get(0) != nil {
		resp = args.Get(0).(*protocol.WsResponse)
	}
	return resp, args.Error(1)
}

func (w *WsProcessorMock) SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error) {
	args := w.Called(ctx, upstreamRequest)
	var ch chan *protocol.WsResponse
	if args.Get(0) != nil {
		ch = args.Get(0).(chan *protocol.WsResponse)
	}
	return ch, args.Error(1)
}

func (w *WsProcessorMock) SubscribeWsStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	args := w.Called(name)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*utils.Subscription[protocol.SubscribeConnectorState])
}
