package mocks

import (
	"context"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
	"time"
)

type HttpConnectorMock struct {
	mock.Mock
}

func NewHttpConnectorMock() *HttpConnectorMock {
	return &HttpConnectorMock{}
}

func (c *HttpConnectorMock) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	args := c.Called(ctx, request)
	return args.Get(0).(protocol.ResponseHolder)
}

func (c *HttpConnectorMock) Subscribe(ctx context.Context, request protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (c *HttpConnectorMock) GetType() protocol.ApiConnectorType {
	return protocol.JsonRpcConnector
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
	var err error
	if args.Get(1) == nil {
		err = nil
	} else {
		err = args.Get(1).(error)
	}
	return args.Get(0).(protocol.UpstreamSubscriptionResponse), err
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
	var err error
	if args.Get(0) == nil {
		err = nil
	} else {
		err = args.Get(0).(error)
	}
	return err
}

func (c *CacheConnectorMock) Receive(ctx context.Context, key string) ([]byte, error) {
	args := c.Called(ctx, key)
	var err error
	if args.Get(1) == nil {
		err = nil
	} else {
		err = args.Get(1).(error)
	}
	return args.Get(0).([]byte), err
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

var _ methods.Methods = (*MethodsMock)(nil)

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

var _ upstreams.UpstreamSupervisor = (*UpstreamSupervisorMock)(nil)
