package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/mock"
)

type WsConnectionMock struct {
	mock.Mock
}

func (w *WsConnectionMock) SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error) {
	args := w.Called(ctx, upstreamRequest)
	var response *protocol.WsResponse
	if args.Get(0) == nil {
		response = nil
	} else {
		response = args.Get(0).(*protocol.WsResponse)
	}
	return response, args.Error(1)
}

func (w *WsConnectionMock) SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error) {
	args := w.Called(ctx, upstreamRequest)
	var responses chan *protocol.WsResponse
	if args.Get(0) == nil {
		responses = nil
	} else {
		responses = args.Get(0).(chan *protocol.WsResponse)
	}
	return responses, args.Error(1)
}

func NewWsConnectionMock() *WsConnectionMock {
	return &WsConnectionMock{}
}
