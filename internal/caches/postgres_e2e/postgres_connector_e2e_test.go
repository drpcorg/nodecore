package caches_postgres_test

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
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	dbName = "nodecore_cache"
	dbUser = "nodecore"
	dbPass = "secret"
)

var fullUrl string
var storageRegistry *storages.StorageRegistry

func TestMain(m *testing.M) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPass),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = postgresContainer.Terminate(context.Background()) }()
	host, _ := postgresContainer.Host(ctx)
	mapped, _ := postgresContainer.MappedPort(ctx, "5432/tcp")
	fullUrl = fmt.Sprintf("postgres://%s:%s@%s:%s/%s", dbUser, dbPass, host, mapped.Port(), dbName)

	storageRegistry, _ = storages.NewStorageRegistry([]config.AppStorageConfig{
		{
			Name: "test-postgres",
			Postgres: &config.PostgresStorageConfig{
				Url: fullUrl,
			},
		},
	})

	code := m.Run()
	os.Exit(code)
}

func TestPostgresConnectorInitialize(t *testing.T) {
	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			StorageName:           "test-postgres",
			QueryTimeout:          lo.ToPtr(1 * time.Second),
			CacheTable:            "cache",
			ExpiredRemoveInterval: 1 * time.Hour,
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorInitialize(t, connector)
}

func TestPostgresConnectorNoItemThenErrCacheNotFound(t *testing.T) {
	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			StorageName:           "test-postgres",
			QueryTimeout:          lo.ToPtr(1 * time.Second),
			CacheTable:            "cache",
			ExpiredRemoveInterval: 1 * time.Hour,
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorNoItemThenErrCacheNotFound(t, connector)
}

func TestPostgresConnectorStoreThenReceive(t *testing.T) {
	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			StorageName:           "test-postgres",
			QueryTimeout:          lo.ToPtr(1 * time.Second),
			CacheTable:            "cache",
			ExpiredRemoveInterval: 1 * time.Hour,
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorStoreThenReceive(t, connector)
}

func TestPostgresConnectorStoreAndRemoveExpired(t *testing.T) {
	connector, err := caches.NewPostgresConnector(
		"id",
		&config.PostgresCacheConnectorConfig{
			StorageName:           "test-postgres",
			QueryTimeout:          lo.ToPtr(1 * time.Second),
			CacheTable:            "cache",
			ExpiredRemoveInterval: 1 * time.Hour,
		},
		storageRegistry,
	)
	assert.Nil(t, err)

	e2e.TestConnectorStoreAndRemoveExpired(t, connector)
}
