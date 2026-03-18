package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/mock"
)

type MockDrpcHttpcConnector struct {
	mock.Mock
}

func NewMockDrpcHttpcConnector() *MockDrpcHttpcConnector {
	return &MockDrpcHttpcConnector{}
}

func (m *MockDrpcHttpcConnector) OwnerExists(ownerId, apiToken string) error {
	args := m.Called(ownerId, apiToken)
	return args.Error(0)
}

func (m *MockDrpcHttpcConnector) LoadOwnerKeys(ownerId, apiToken string) ([]*drpc.DrpcKey, error) {
	args := m.Called(ownerId, apiToken)
	var keys []*drpc.DrpcKey
	if args.Get(0) == nil {
		keys = nil
	} else {
		keys = args.Get(0).([]*drpc.DrpcKey)
	}
	return keys, args.Error(1)
}

type ConnectorMock struct {
	mock.Mock
	connectorType protocol.ApiConnectorType
}

func NewConnectorMock() *ConnectorMock {
	return &ConnectorMock{connectorType: protocol.JsonRpcConnector}
}

func NewConnectorMockWithType(connectorType protocol.ApiConnectorType) *ConnectorMock {
	return &ConnectorMock{connectorType: connectorType}
}

func (c *ConnectorMock) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	args := c.Called(ctx, request)
	return args.Get(0).(protocol.ResponseHolder)
}

func (c *ConnectorMock) Subscribe(ctx context.Context, request protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (c *ConnectorMock) GetType() protocol.ApiConnectorType {
	return c.connectorType
}

type WsConnectorMock struct {
	mock.Mock
}

func NewWsConnectorMock() *WsConnectorMock {
	return &WsConnectorMock{}
}

func (c *WsConnectorMock) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	return nil
}

func (c *WsConnectorMock) Subscribe(ctx context.Context, request protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	args := c.Called(ctx, request)
	var response protocol.UpstreamSubscriptionResponse
	if args.Get(0) == nil {
		response = nil
	} else {
		response = args.Get(0).(protocol.UpstreamSubscriptionResponse)
	}
	return response, args.Error(1)
}

func (c *WsConnectorMock) GetType() protocol.ApiConnectorType {
	return protocol.WsConnector
}
