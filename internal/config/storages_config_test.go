package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisDefaults(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-valid-defaults.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	redisCfg := appCfg.CacheConfig.CacheConnectors[0].Redis
	expected := &config.RedisCacheConnectorConfig{
		StorageName: "redis-storage-defaults",
	}

	assert.Equal(t, expected, redisCfg)
}

func TestRedisFullCustom(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-full-custom.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	redisCfg := appCfg.CacheConfig.CacheConnectors[0].Redis
	expected := &config.RedisCacheConnectorConfig{
		StorageName: "redis-storage-custom",
	}

	assert.Equal(t, expected, redisCfg)
}

func TestRedisMissingAddressThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-missing-address.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: either 'address' or 'full_url' must be specified")
}

func TestRedisNegativeReadTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-read-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: read timeout cannot be negative")
}

func TestRedisNegativeWriteTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-write-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: write timeout cannot be negative")
}

func TestRedisNegativeConnectTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-connect-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: connect timeout cannot be negative")
}

func TestRedisPoolNegativeSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool size cannot be negative")
}

func TestRedisPoolNegativePoolTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-pool-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool timeout cannot be negative")
}

func TestRedisPoolMinGreaterMaxIdleThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-min-greater-max.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool min idle connections cannot be greater than pool max idle connections")
}

func TestRedisPoolNegativeMaxActiveThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-max-active.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool max connections cannot be negative")
}

func TestRedisPoolNegativeConnLifeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-conn-life.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool conn max life time cannot be negative")
}

func TestRedisPoolNegativeConnIdleThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-conn-idle.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during redis storage config validation, cause: pool conn max idle time cannot be negative")
}

func TestPostgresDefaults(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-valid-defaults.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	pg := appCfg.CacheConfig.CacheConnectors[0].Postgres
	expected := &config.PostgresCacheConnectorConfig{
		StorageName:           "postgres-storage-defaults",
		ExpiredRemoveInterval: 30 * time.Second,
		QueryTimeout:          lo.ToPtr(300 * time.Millisecond),
		CacheTable:            "cache_rpc",
	}

	assert.Equal(t, expected, pg)
}

func TestPostgresFullCustom(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-full-custom.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	pg := appCfg.CacheConfig.CacheConnectors[0].Postgres
	expected := &config.PostgresCacheConnectorConfig{
		StorageName:           "postgres-storage-custom",
		ExpiredRemoveInterval: 1 * time.Minute,
		QueryTimeout:          lo.ToPtr(2 * time.Second),
		CacheTable:            "cache_custom",
	}

	assert.Equal(t, expected, pg)
}

func TestDuplicateStorageNamesThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/duplicate-storage-names.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "duplicate storage name 'redis-storage'")
}
