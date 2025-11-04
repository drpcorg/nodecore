package caches_redis_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/drpcorg/nodecore/pkg/test_utils/e2e"
	"github.com/stretchr/testify/assert"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var fullUrl string
var storageRegistry *storages.StorageRegistry

func TestMain(m *testing.M) {
	ctx := context.Background()

	req := tc.ContainerRequest{
		Image:        "redis:8.2.1-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Cmd: []string{
			"redis-server",
			"--appendonly", "yes",
		},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	redisC, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = redisC.Terminate(context.Background()) }()

	mp, _ := redisC.MappedPort(ctx, "6379/tcp")
	fullUrl = fmt.Sprintf("redis://localhost:%s/0?read_timeout=3000ms", mp.Port())

	storageRegistry, _ = storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-redis",
			Redis: &config.RedisStorageConfig{
				FullUrl: fullUrl,
			},
		},
	})

	code := m.Run()
	os.Exit(code)
}

func TestRedisConnectorInitialize(t *testing.T) {
	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			StorageName: "test-redis",
		},
		storageRegistry,
	)
	assert.NoError(t, err)

	e2e.TestConnectorInitialize(t, connector)
}

func TestRedisConnectorNoItemThenErrCacheNotFound(t *testing.T) {
	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			StorageName: "test-redis",
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorNoItemThenErrCacheNotFound(t, connector)
}

func TestRedisConnectorStoreThenReceive(t *testing.T) {
	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			StorageName: "test-redis",
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorStoreThenReceive(t, connector)
}

func TestRedisConnectorStoreAndRemoveExpired(t *testing.T) {
	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			StorageName: "test-redis",
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorStoreAndRemoveExpired(t, connector)
}
