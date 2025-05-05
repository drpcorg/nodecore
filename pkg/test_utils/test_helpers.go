package test_utils

import (
	"context"
	json2 "encoding/json"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/stretchr/testify/mock"
	"time"
)

func GetResultAsBytes(json []byte) []byte {
	var parsed map[string]json2.RawMessage
	err := sonic.Unmarshal(json, &parsed)
	if err != nil {
		panic(err)
	}
	return parsed["result"]
}

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

func PolicyConfig(chain, method, connector, maxSize, ttl string, cacheEmpty bool) *config.CachePolicyConfig {
	return &config.CachePolicyConfig{
		Id:               "policy",
		Chain:            chain,
		Method:           method,
		FinalizationType: config.None,
		CacheEmpty:       cacheEmpty,
		Connector:        connector,
		ObjectMaxSize:    maxSize,
		TTL:              ttl,
	}
}
