package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheConnectorNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-no-connector-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connectors validation, cause: no connector id under index 0")
}

func TestCacheConnectorWrongDriverThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-driver.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'test' validation, cause: invalid cache driver - 'wrong-driver'")
}

func TestCacheConnectorWrongMemoryMaxItemsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-memory-max-items.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'test' validation, cause: memory max items must be > 0")
}

func TestCacheConnectorDuplicateIdsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-duplicate-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connectors validation, connector with id 'test' already exists")
}

func TestCachePolicyNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-policy-no-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policies validation, cause: no policy id under index 0")
}

func TestCachePolicyNoChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: empty chain setting")
}

func TestCachePolicyNotSupportedChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-not-supported-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: chain 'not-supported' is not supported")
}

func TestCachePolicyWrongMaxSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: size must be in KB or MB")
}

func TestCachePolicyZeroMaxSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-zero-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: size must be > 0")
}

func TestCachePolicyEmptyMethodThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-method.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: empty method setting")
}

func TestCachePolicyEmptyConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-policy-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: empty connector")
}

func TestCachePolicyWrongFinalizationThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-finalization.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: invalid finalization type - 'wrong-type'")
}

func TestCachePolicyWrongTtlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-ttl.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: time: missing unit in duration \"10\"")
}

func TestCachePolicyNotExistedConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-not-existed-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: there is no such connector - 'strange-connector'")
}

func TestIfNoCacheSettingsThenNil(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-no-cache-setting.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, &config.CacheConfig{ReceiveTimeout: 1 * time.Second}, appConfig.CacheConfig)
}

func TestDefaultMemorySettings(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-default-memory-settings.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.MemoryCacheConnectorConfig{
		MaxItems:              10000,
		ExpiredRemoveInterval: 30 * time.Second,
	}

	assert.Equal(t, expected, appConfig.CacheConfig.CacheConnectors[0].Memory)
}

func TestDefaultPolicySettings(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-default-policy-settings.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.CachePolicyConfig{
		Id:               "my_policy",
		Chain:            "ethereum",
		Method:           "*getBlock*",
		FinalizationType: config.None,
		CacheEmpty:       false,
		Connector:        "test",
		ObjectMaxSize:    "500KB",
		TTL:              "10m",
	}

	assert.Equal(t, appConfig.CacheConfig.CachePolicies[0], expected)
}

func TestDefaultPolicySettingsWithZeroTtl(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-default-policy-settings-ttl.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.CachePolicyConfig{
		Id:               "my_policy",
		Chain:            "ethereum",
		Method:           "*getBlock*",
		FinalizationType: config.None,
		CacheEmpty:       false,
		Connector:        "test",
		ObjectMaxSize:    "500KB",
		TTL:              "0s",
	}

	assert.Equal(t, appConfig.CacheConfig.CachePolicies[0], expected)
}

func TestPostgresMissingUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-missing-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "postgres storage name 'nonexistent-storage' not found")
}

func TestPostgresNegativeQueryTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-negative-query-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'pg4' validation, cause: query-timeout must be greater than or equal to 0")
}

func TestPostgresNonpositiveExpiredIntervalThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-nonpositive-expired-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'pg5' validation, cause: expired remove interval must be > 0")
}
