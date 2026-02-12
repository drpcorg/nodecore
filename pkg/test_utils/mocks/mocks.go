package mocks

import (
	"context"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
)

type MockIntegrationClient struct {
	mock.Mock
	integrationType integration.IntegrationType
}

func NewMockIntegrationClient(integrationType integration.IntegrationType) *MockIntegrationClient {
	return &MockIntegrationClient{
		integrationType: integrationType,
	}
}

func (m *MockIntegrationClient) InitKeys(cfg config.IntegrationKeyConfig) (chan integration.KeyEvent, error) {
	args := m.Called(cfg)
	var data chan integration.KeyEvent
	if args.Get(0) == nil {
		data = nil
	} else {
		data = args.Get(0).(chan integration.KeyEvent)
	}
	return data, args.Error(1)
}

func (m *MockIntegrationClient) Type() integration.IntegrationType {
	return m.integrationType
}

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

type MockAuthProcessor struct {
	mock.Mock
}

func NewMockAuthProcessor() *MockAuthProcessor {
	return &MockAuthProcessor{}
}

func (m *MockAuthProcessor) Authenticate(ctx context.Context, payload auth.AuthPayload) error {
	args := m.Called(ctx, payload)
	return args.Error(0)
}

func (m *MockAuthProcessor) PreKeyValidate(ctx context.Context, payload auth.AuthPayload) ([]string, error) {
	args := m.Called(ctx, payload)
	var cors []string
	if args.Get(0) == nil {
		cors = nil
	} else {
		cors = args.Get(0).([]string)
	}
	return cors, args.Error(1)
}

func (m *MockAuthProcessor) PostKeyValidate(ctx context.Context, payload auth.AuthPayload, request protocol.RequestHolder) error {
	args := m.Called(ctx, payload, request)
	return args.Error(0)
}

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

func (c *CacheConnectorMock) Initialize() error {
	args := c.Called()
	return args.Error(0)
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
	if args.Get(0) == nil {
		return nil
	}

	return args.Get(0).(*upstreams.ChainSupervisor)
}

func (u *UpstreamSupervisorMock) GetUpstream(id string) upstreams.Upstream {
	args := u.Called(id)

	return args.Get(0).(*upstreams.BaseUpstream)
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

type SettingsValidatorMock struct {
	mock.Mock
}

func NewSettingsValidatorMock() *SettingsValidatorMock {
	return &SettingsValidatorMock{}
}

func (s *SettingsValidatorMock) Validate() validations.ValidationSettingResult {
	return s.Called().Get(0).(validations.ValidationSettingResult)
}
