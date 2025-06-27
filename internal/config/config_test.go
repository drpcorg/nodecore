package config_test

import (
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNoConfigFileThenError(t *testing.T) {
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "open ./dSheltie.yml: no such file or directory")
}

func TestReadFullConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/valid-full-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		ServerConfig: &config.ServerConfig{
			Port: 9095,
		},
		CacheConfig: &config.CacheConfig{
			CacheConnectors: []*config.CacheConnectorConfig{
				{
					Id:     "memory-connector",
					Driver: config.Memory,
					Memory: &config.MemoryCacheConnectorConfig{
						MaxItems:              1000,
						ExpiredRemoveInterval: 10 * time.Second,
					},
				},
			},
			CachePolicies: []*config.CachePolicyConfig{
				{
					Id:               "super_policy",
					Chain:            "optimism|polygon | ethereum",
					Method:           "*getBlock*",
					FinalizationType: config.None,
					CacheEmpty:       true,
					Connector:        "memory-connector",
					ObjectMaxSize:    "10KB",
					TTL:              "10s",
				},
			},
		},
		UpstreamConfig: &config.UpstreamConfig{
			ScorePolicyConfig: &config.ScorePolicyConfig{
				CalculationInterval: 10 * time.Second,
				CalculationFunction: config.DefaultLatencyPolicyFunc,
			},
			FailsafeConfig: &config.FailsafeConfig{
				HedgeConfig: &config.HedgeConfig{
					Delay: 500 * time.Millisecond,
					Count: 2,
				},
			},
			ChainDefaults: map[string]*config.ChainDefaults{
				"ethereum": {
					PollInterval: 2 * time.Minute,
				},
			},
			Upstreams: []*config.Upstream{
				{
					Id:            "eth-upstream",
					HeadConnector: config.Ws,
					PollInterval:  2 * time.Minute,
					ChainName:     "ethereum",
					Methods:       &config.MethodsConfig{},
					Connectors: []*config.ApiConnectorConfig{
						{
							Type: config.JsonRpc,
							Url:  "https://test.com",
							Headers: map[string]string{
								"Key": "Value",
							},
						},
						{
							Type: config.Ws,
							Url:  "wss://test.com",
						},
					},
					FailsafeConfig: &config.FailsafeConfig{},
				},
				{
					Id:             "another",
					HeadConnector:  config.Rest,
					PollInterval:   1 * time.Minute,
					ChainName:      "polygon",
					Methods:        &config.MethodsConfig{},
					FailsafeConfig: &config.FailsafeConfig{},
					Connectors: []*config.ApiConnectorConfig{
						{
							Type: config.Rest,
							Url:  "https://test.com",
						},
						{
							Type: config.Grpc,
							Url:  "https://test-grpc.com",
							Headers: map[string]string{
								"key": "value",
							},
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}

func TestNoSupportedChainDefaultsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/not-supported-chain-defaults.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during chain defaults validation, cause: not supported chain no-chain")
}

func TestNoUpstreamIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-upstream-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream validation, cause: no upstream id under index 1")
}

func TestDuplicateIdsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/duplicate-ids.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream validation, cause: upstream with id another already exists")
}

func TestNoSupportedUpstreamChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/not-supported-up-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: not supported chain wrong")
}

func TestInvalidHeadConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-head-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: invalid head connector")
}

func TestNoConnectorsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: there must be at least one upstream connector")
}

func TestWrongHeadConnectorError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/wrong-head-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "there is no json-rpc connector for head")
}

func TestDuplicateConnectorsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/duplicate-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: there can be only one connector of type json-rpc")
}

func TestInvalidConnectorTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: invalid connector type - wrong-type")
}

func TestNoConnectorUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: url must be specified for connector rest")
}

func TestSetDefaultPollInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/default-poll-interval.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.Upstream{
		Id:             "eth-upstream",
		Methods:        &config.MethodsConfig{},
		HeadConnector:  config.JsonRpc,
		PollInterval:   1 * time.Minute,
		ChainName:      "ethereum",
		FailsafeConfig: &config.FailsafeConfig{},
		Connectors: []*config.ApiConnectorConfig{
			{
				Type: config.JsonRpc,
				Url:  "https://test.com",
			},
		},
	}

	assert.Equal(t, expected, appConfig.UpstreamConfig.Upstreams[0])
}

func TestSetDefaultJsonRpcHeadConnector(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/default-head-connector.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.Upstream{
		Id:             "eth-upstream",
		HeadConnector:  config.JsonRpc,
		PollInterval:   1 * time.Minute,
		ChainName:      "ethereum",
		Methods:        &config.MethodsConfig{},
		FailsafeConfig: &config.FailsafeConfig{},
		Connectors: []*config.ApiConnectorConfig{
			{
				Type: config.JsonRpc,
				Url:  "https://test.com",
			},
			{
				Type: config.Rest,
				Url:  "https://test.com",
			},
		},
	}

	assert.Equal(t, expected, appConfig.UpstreamConfig.Upstreams[0])
}

func TestSetDefaultRestHeadConnector(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/default-rest-head-connector.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.Upstream{
		Id:             "eth-upstream",
		HeadConnector:  config.Rest,
		PollInterval:   1 * time.Minute,
		ChainName:      "ethereum",
		Methods:        &config.MethodsConfig{},
		FailsafeConfig: &config.FailsafeConfig{},
		Connectors: []*config.ApiConnectorConfig{
			{
				Type: config.Ws,
				Url:  "https://test.com",
			},
			{
				Type: config.Rest,
				Url:  "https://test.com",
			},
		},
	}

	assert.Equal(t, expected, appConfig.UpstreamConfig.Upstreams[0])
}

func TestServerConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := config.ServerConfig{
		Port: 9095,
	}

	assert.Equal(t, &expected, appConfig.ServerConfig)
}

func TestSetChainsDefault(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/default-from-chains.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			ChainDefaults: map[string]*config.ChainDefaults{
				"ethereum": {
					PollInterval: 10 * time.Minute,
				},
			},
			Upstreams: []*config.Upstream{
				{
					Id:             "eth-upstream",
					Methods:        &config.MethodsConfig{},
					HeadConnector:  config.JsonRpc,
					PollInterval:   10 * time.Minute,
					ChainName:      "ethereum",
					FailsafeConfig: &config.FailsafeConfig{},
					Connectors: []*config.ApiConnectorConfig{
						{
							Type: config.JsonRpc,
							Url:  "https://test.com",
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected.UpstreamConfig.Upstreams[0], appConfig.UpstreamConfig.Upstreams[0])
	assert.Equal(t, expected.UpstreamConfig.ChainDefaults, appConfig.UpstreamConfig.ChainDefaults)
}

func TestCacheConnectorNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-no-connector-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connectors validation, cause: no connector id under index 0")
}

func TestCacheConnectorWrongDriverThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-driver.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector test validation, cause: invalid cache driver - wrong-driver")
}

func TestCacheConnectorWrongMemoryMaxItemsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-memory-max-items.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector test validation, cause: memory max items must be > 0")
}

func TestCacheConnectorDuplicateIdsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-duplicate-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connectors validation, connector with id test already exists")
}

func TestCachePolicyNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-policy-no-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policies validation, cause: no policy id under index 0")
}

func TestCachePolicyNoChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: empty chain setting")
}

func TestCachePolicyNotSupportedChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-not-supported-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: chain eth is not supported")
}

func TestCachePolicyWrongMaxSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: size must be in KB or MB")
}

func TestCachePolicyZeroMaxSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-zero-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: size must be > 0")
}

func TestCachePolicyEmptyMethodThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-method.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: empty method setting")
}

func TestCachePolicyEmptyConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-empty-policy-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: empty connector")
}

func TestCachePolicyWrongFinalizationThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-finalization.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: invalid finalization type - wrong-type")
}

func TestCachePolicyWrongTtlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-wrong-ttl.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: time: missing unit in duration \"10\"")
}

func TestCachePolicyNotExistedConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-not-existed-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache policy my_policy validation, cause: there is no such connector - strange-connector")
}

func TestIfNoCacheSettingsThenNil(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-no-cache-setting.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, &config.CacheConfig{}, appConfig.CacheConfig)
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

func TestScorePolicyConfigFilePath(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/score-policy-file-path.yaml")
	appCfg, err := config.NewAppConfig()

	expected := &config.ScorePolicyConfig{
		CalculationInterval:         10 * time.Second,
		CalculationFunctionFilePath: "configs/upstreams/func.ts",
	}

	assert.Nil(t, err)
	assert.Equal(t, expected, appCfg.UpstreamConfig.ScorePolicyConfig)
}

func TestScorePolicyConfigBothCalculationFuncAndFilePathThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-score-policy-both-params.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during score policy config, cause: one setting must be specified - either 'calculation-function' or 'calculation-function-file-path'`)
}

func TestScorePolicyConfigInvalidIntervalThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-score-policy-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during score policy config, cause: the calculation interval can't be less than 0")
}

func TestScorePolicyConfigNoSortFuncThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-score-policy-func.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during score policy config, cause: couldn't read a ts script, no sortUpstreams() function in the specified script")
}

func TestScorePolicyConfigTypoInScriptThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-score-policy-typo-sortUpstream.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during score policy config, cause: couldn't read a ts script, Expected ";" but found "0"`)
}
