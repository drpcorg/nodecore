package caches_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/stretchr/testify/assert"
)

func TestRedisConnectorCantParseUrl(t *testing.T) {
	storageRegistry, err := storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-redis",
			Redis: &config.RedisStorageConfig{
				FullUrl: "http://url.com",
			},
		},
	})
	assert.ErrorContains(t, err, "couldn't read the full redis url http://url.com of storage 'test-redis': redis: invalid URL scheme: http")
	assert.Nil(t, storageRegistry)
}

func TestRedisConnectorCantPing(t *testing.T) {
	storageRegistry, err := storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-redis",
			Redis: &config.RedisStorageConfig{
				FullUrl: "redis://:testPass@localhost:9000/0?read_timeout=3000ms",
			},
		},
	})
	assert.Nil(t, err)

	connector, err := caches.NewRedisConnector(
		"id",
		&config.RedisCacheConnectorConfig{
			StorageName: "test-redis",
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	err = connector.Initialize()
	assert.ErrorContains(t, err, "could not connect to redis, dial tcp [::1]:9000: connect: connection refused")
}
