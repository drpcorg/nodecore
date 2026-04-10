package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type MockDrpcHttpcConnector struct {
	mock.Mock
}

func (m *MockDrpcHttpcConnector) UploadStats(stats []byte, ownerId, apiToken string) error {
	return nil
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

func (c *ConnectorMock) SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	args := c.Called(name)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*utils.Subscription[protocol.SubscribeConnectorState])
}

func (c *ConnectorMock) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	args := c.Called(ctx, request)
	return args.Get(0).(protocol.ResponseHolder)
}

func (c *ConnectorMock) Subscribe(ctx context.Context, request protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	args := c.Called(ctx, request)
	var response protocol.UpstreamSubscriptionResponse
	if args.Get(0) != nil {
		response = args.Get(0).(protocol.UpstreamSubscriptionResponse)
	}
	return response, args.Error(1)
}

func (c *ConnectorMock) Unsubscribe(opId string) {
	c.Called(opId)
}

func (c *ConnectorMock) Start() {
	c.Called()
}

func (c *ConnectorMock) Stop() {
	c.Called()
}

func (c *ConnectorMock) Running() bool {
	args := c.Called()
	return args.Bool(0)
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

func (c *WsConnectorMock) Start() {
	c.Called()
}

func (c *WsConnectorMock) Stop() {
	c.Called()
}

func (c *WsConnectorMock) Running() bool {
	args := c.Called()
	return args.Bool(0)
}

func (c *WsConnectorMock) SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	args := c.Called(name)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*utils.Subscription[protocol.SubscribeConnectorState])
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

func (c *WsConnectorMock) Unsubscribe(opId string) {
	c.Called(opId)
}

func (c *WsConnectorMock) GetType() protocol.ApiConnectorType {
	return protocol.WsConnector
}
