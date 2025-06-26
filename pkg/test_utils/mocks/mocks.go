package mocks

import (
	"context"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
	"time"
)

type MockStrategy struct {
	mock.Mock
}

func (m *MockStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	args := m.Called(request)
	return args.Get(0).(string), args.Error(1)
}

func NewMockStrategy() *MockStrategy {
	return &MockStrategy{}
}

type CacheProcessorMock struct {
	mock.Mock
}

func (c *CacheProcessorMock) Store(ctx context.Context, chain chains.Chain, request protocol.RequestHolder, response []byte) {
	c.Called(ctx, chain, request, response)
}

func (c *CacheProcessorMock) Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool) {
	args := c.Called(ctx, chain, request)
	return args.Get(0).([]byte), args.Get(1).(bool)
}

func NewCacheProcessorMock() *CacheProcessorMock {
	return &CacheProcessorMock{}
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

type CacheConnectorMock struct {
	mock.Mock
}

func NewCacheConnectorMock() *CacheConnectorMock {
	return &CacheConnectorMock{}
}

func (c *CacheConnectorMock) Id() string {
	args := c.Called()
	return args.Get(0).(string)
}

func (c *CacheConnectorMock) Store(ctx context.Context, key string, object string, ttl time.Duration) error {
	args := c.Called(ctx, key, object, ttl)
	return args.Error(0)
}

func (c *CacheConnectorMock) Receive(ctx context.Context, key string) ([]byte, error) {
	args := c.Called(ctx, key)
	return args.Get(0).([]byte), args.Error(1)
}

type DelayedConnector struct {
	sleep time.Duration
	CacheConnectorMock
}

func NewDelayedConnector(sleep time.Duration) *DelayedConnector {
	return &DelayedConnector{sleep: sleep}
}

func (d *DelayedConnector) Id() string {
	return d.CacheConnectorMock.Id()
}

func (d *DelayedConnector) Store(ctx context.Context, key string, object string, ttl time.Duration) error {
	time.Sleep(d.sleep)
	return d.CacheConnectorMock.Store(ctx, key, object, ttl)
}

func (d *DelayedConnector) Receive(ctx context.Context, key string) ([]byte, error) {
	time.Sleep(d.sleep)
	return d.CacheConnectorMock.Receive(ctx, key)
}

type MethodsMock struct {
	mock.Mock
}

func NewMethodsMock() *MethodsMock {
	return &MethodsMock{}
}

func (m *MethodsMock) GetMethod(methodName string) *specs.Method {
	args := m.Called(methodName)
	if args.Get(0) == nil {
		return nil
	}

	return args.Get(0).(*specs.Method)
}

func (m *MethodsMock) GetSupportedMethods() mapset.Set[string] {
	args := m.Called()

	return args.Get(0).(mapset.Set[string])
}

func (m *MethodsMock) HasMethod(s string) bool {
	args := m.Called(s)

	return args.Get(0).(bool)
}

func (m *MethodsMock) IsCacheable(ctx context.Context, data any, methodName string) bool {
	args := m.Called(data, methodName)

	return args.Get(0).(bool)
}

type UpstreamSupervisorMock struct {
	mock.Mock
}

func NewUpstreamSupervisorMock() *UpstreamSupervisorMock {
	return &UpstreamSupervisorMock{}
}

func (u *UpstreamSupervisorMock) GetChainSupervisors() []*upstreams.ChainSupervisor {
	args := u.Called()

	return args.Get(0).([]*upstreams.ChainSupervisor)
}

func (u *UpstreamSupervisorMock) GetChainSupervisor(chain chains.Chain) *upstreams.ChainSupervisor {
	args := u.Called(chain)

	return args.Get(0).(*upstreams.ChainSupervisor)
}

func (u *UpstreamSupervisorMock) GetUpstream(id string) *upstreams.Upstream {
	args := u.Called(id)

	return args.Get(0).(*upstreams.Upstream)
}

func (u *UpstreamSupervisorMock) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	args := u.Called()

	return args.Get(0).(failsafe.Executor[*protocol.ResponseHolderWrapper])
}

func (u *UpstreamSupervisorMock) StartUpstreams() {
	u.Called()
}

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
