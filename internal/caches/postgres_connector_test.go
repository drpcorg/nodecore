package caches_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestPostgresConnectorCantParseUrl(t *testing.T) {
	storageRegistry, err := storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-postgres",
			Postgres: &config.PostgresStorageConfig{
				Url: "http://url.com",
			},
		},
	})
	assert.ErrorContains(t, err, "couldn't parse postgres url")
	assert.Nil(t, storageRegistry)
}

func TestPostgresConnectorCantCreatePool(t *testing.T) {
	storageRegistry, err := storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-postgres",
			Postgres: &config.PostgresStorageConfig{
				Url: "postgres://user:user@localhost:3000/cache",
			},
		},
	})
	assert.Nil(t, err)

	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			StorageName:           "test-postgres",
			QueryTimeout:          lo.ToPtr(1 * time.Second),
			CacheTable:            "cache_table",
			ExpiredRemoveInterval: 1 * time.Hour,
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	err = connector.Initialize()
	assert.ErrorContains(t, err, "couldn't connect to postgres")
}
