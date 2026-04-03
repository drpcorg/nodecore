package mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/mock"
)

type CacheProcessorMock struct {
	mock.Mock
}

func (c *CacheProcessorMock) OutboxStore(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return fmt.Errorf("not implemented")
}

func (c *CacheProcessorMock) OutboxRemove(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func (c *CacheProcessorMock) OutboxList(_ context.Context, _, _ int64) ([]map[string][]byte, error) {
	return nil, fmt.Errorf("not implemented")
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

type CacheConnectorMock struct {
	mock.Mock
}

func (c *CacheConnectorMock) OutboxStore(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return nil
}

func (c *CacheConnectorMock) OutboxRemove(ctx context.Context, key string) error {
	return nil
}

func (c *CacheConnectorMock) OutboxList(ctx context.Context, cursor, limit int64) ([]map[string][]byte, error) {
	return nil, nil
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
