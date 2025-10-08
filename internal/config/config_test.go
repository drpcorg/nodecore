package config_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoConfigFileThenError(t *testing.T) {
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "open ./nodecore.yml: no such file or directory")
}

func TestReadFullConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/valid-full-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		ServerConfig: &config.ServerConfig{
			Port: 9095,
			PyroscopeConfig: &config.PyroscopeConfig{
				Enabled:  true,
				Url:      "url",
				Username: "pyro-username",
				Password: "pyro-password",
			},
			TlsConfig: &config.TlsConfig{
				Enabled:     true,
				Certificate: "/path/cert",
				Key:         "/path/key",
				Ca:          "/path/ca",
			},
		},
		AuthConfig: &config.AuthConfig{
			Enabled: true,
			RequestStrategyConfig: &config.RequestStrategyConfig{
				Type: config.Jwt,
				JwtRequestStrategyConfig: &config.JwtRequestStrategyConfig{
					PublicKey:          "/path/to/key",
					AllowedIssuer:      "my-super",
					ExpirationRequired: true,
				},
			},
			KeyConfigs: []*config.KeyConfig{
				{
					Id:   "other-key",
					Type: config.Local,
					LocalKeyConfig: &config.LocalKeyConfig{
						Key: "asadasdjabdhjabshjd",
						KeySettingsConfig: &config.KeySettingsConfig{
							AllowedIps: []string{"192.0.0.1", "127.0.0.1"},
							Methods: &config.AuthMethods{
								Allowed:   []string{"eth_test"},
								Forbidden: []string{"eth_syncing"},
							},
							AuthContracts: &config.AuthContracts{
								Allowed: []string{"0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"},
							},
						},
					},
				},
			},
		},
		CacheConfig: &config.CacheConfig{
			ReceiveTimeout: 1 * time.Second,
			CacheConnectors: []*config.CacheConnectorConfig{
				{
					Id:     "memory-connector",
					Driver: config.Memory,
					Memory: &config.MemoryCacheConnectorConfig{
						MaxItems:              1000,
						ExpiredRemoveInterval: 10 * time.Second,
					},
				},
				{
					Id:     "redis-connector",
					Driver: config.Redis,
					Redis: &config.RedisCacheConnectorConfig{
						FullUrl:  "redis://localhost:6379/0",
						Address:  "localhost:6379",
						Username: "username",
						Password: "password",
						DB:       lo.ToPtr(2),
						Timeouts: &config.RedisCacheConnectorTimeoutsConfig{
							ConnectTimeout: lo.ToPtr(1 * time.Second),
							ReadTimeout:    lo.ToPtr(2 * time.Second),
							WriteTimeout:   lo.ToPtr(3 * time.Second),
						},
						Pool: &config.RedisCacheConnectorPoolConfig{
							Size:            35,
							PoolTimeout:     lo.ToPtr(5 * time.Second),
							MinIdleConns:    10,
							MaxIdleConns:    50,
							MaxActiveConns:  45,
							ConnMaxIdleTime: lo.ToPtr(60 * time.Second),
							ConnMaxLifeTime: lo.ToPtr(60 * time.Minute),
						},
					},
				},
				{
					Id:     "postgresql-connector",
					Driver: config.Postgres,
					Postgres: &config.PostgresCacheConnectorConfig{
						Url:                   "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
						QueryTimeout:          lo.ToPtr(5 * time.Second),
						CacheTable:            "cache",
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
			IntegrityConfig: &config.IntegrityConfig{},
			ScorePolicyConfig: &config.ScorePolicyConfig{
				CalculationInterval:     10 * time.Second,
				CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
			},
			FailsafeConfig: &config.FailsafeConfig{
				RetryConfig: &config.RetryConfig{
					Attempts: 10,
					Delay:    2 * time.Second,
					MaxDelay: lo.ToPtr(5 * time.Second),
					Jitter:   lo.ToPtr(3 * time.Second),
				},
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
					PollInterval:  3 * time.Minute,
					ChainName:     "ethereum",
					Methods: &config.MethodsConfig{
						BanDuration: 5 * time.Minute,
					},
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
					FailsafeConfig: &config.FailsafeConfig{
						RetryConfig: &config.RetryConfig{
							MaxDelay: lo.ToPtr(1 * time.Second),
							Attempts: 5,
							Delay:    500 * time.Millisecond,
							Jitter:   lo.ToPtr(6 * time.Second),
						},
					},
				},
				{
					Id:            "another",
					HeadConnector: config.Rest,
					PollInterval:  1 * time.Minute,
					ChainName:     "polygon",
					Methods: &config.MethodsConfig{
						BanDuration: 5 * time.Minute,
					},
					FailsafeConfig: &config.FailsafeConfig{
						RetryConfig: &config.RetryConfig{
							Attempts: 7,
							Delay:    300 * time.Millisecond,
						},
					},
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
	assert.ErrorContains(t, err, "error during upstream validation, cause: upstream with id 'another' already exists")
}

func TestNoSupportedUpstreamChainThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/not-supported-up-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: not supported chain 'wrong'")
}

func TestInvalidHeadConnectorThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-head-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: invalid head connector")
}

func TestNoConnectorsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: there must be at least one upstream connector")
}

func TestWrongHeadConnectorError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/wrong-head-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "there is no 'json-rpc' connector for head")
}

func TestDuplicateConnectorsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/duplicate-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: there can be only one connector of type 'json-rpc'")
}

func TestInvalidConnectorTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: invalid connector type - 'wrong-type'")
}

func TestNoConnectorUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: url must be specified for connector 'rest'")
}

func TestSetDefaultPollInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/default-poll-interval.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.Upstream{
		Id: "eth-upstream",
		Methods: &config.MethodsConfig{
			BanDuration: 5 * time.Minute,
		},
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
		Id:            "eth-upstream",
		HeadConnector: config.JsonRpc,
		PollInterval:  1 * time.Minute,
		ChainName:     "ethereum",
		Methods: &config.MethodsConfig{
			BanDuration: 5 * time.Minute,
		},
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
		Id:            "eth-upstream",
		HeadConnector: config.Rest,
		PollInterval:  1 * time.Minute,
		ChainName:     "ethereum",
		Methods: &config.MethodsConfig{
			BanDuration: 5 * time.Minute,
		},
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
		Port:            9095,
		MetricsPort:     9093,
		PprofPort:       6061,
		PyroscopeConfig: &config.PyroscopeConfig{},
		TlsConfig:       &config.TlsConfig{},
	}

	assert.Equal(t, &expected, appConfig.ServerConfig)
}

func TestServerConfigEqualMetricsPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-equal-ports.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "metrics port 9095 is already in use")
}

func TestServerConfigEqualPprofPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-equal-pprof-ports.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "pprof port 8094 is already in use")
}

func TestServerConfigWrongServerPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-wrong-server-port.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "incorrect server port - -9095")
}

func TestServerConfigWrongMetricsPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-wrong-metrics-port.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "incorrect metrics port - -23555")
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
					Id: "eth-upstream",
					Methods: &config.MethodsConfig{
						BanDuration: 5 * time.Minute,
					},
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
	assert.ErrorContains(t, err, "error during cache policy 'my_policy' validation, cause: chain 'eth' is not supported")
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
	assert.ErrorContains(t, err, `error during score policy config validation, cause: one setting must be specified - either 'calculation-function' or 'calculation-function-file-path'`)
}

func TestScorePolicyConfigInvalidIntervalThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-score-policy-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during score policy config validation, cause: the calculation interval can't be less than 0")
}

func TestScorePolicyConfigNoSortFuncThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/no-score-policy-func.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during score policy config validation, cause: 'not-existed' default function doesn't exist")
}

func TestScorePolicyConfigTypoInScriptThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-score-policy-typo-sortUpstream.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during score policy config validation, cause: couldn't read a ts script, Expected ";" but found "0"`)
}

func TestRetryConfigAttemptsLess1ThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/retry-config-attempts-less-1.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during upstream 'eth-upstream' validation, cause: retry config validation error - the number of attempts can't be less than 1`)
}

func TestRetryConfigMaxDelaysIsZeroThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/retry-config-max-delay-0.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during upstream 'eth-upstream' validation, cause: retry config validation error - the retry max delay can't be less than 0`)
}

func TestRetryConfigJitterIsZeroThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/retry-config-jitter-0.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during upstream 'eth-upstream' validation, cause: retry config validation error - the retry jitter can't be 0`)
}

func TestRetryConfigDelayGreaterMaxDelayThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/retry-config-delay-greater-max-delay.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `error during upstream 'eth-upstream' validation, cause: retry config validation error - the retry delay can't be greater than the retry max delay`)
}

func TestPyroConfigNoUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-pyro-no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, url must be specified`)
}

func TestPyroConfigNoUsernameThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-pyro-no-username.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, username must be specified`)
}

func TestPyroConfigNoPasswordThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-pyro-no-password.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, password must be specified`)
}

func TestAuthInvalidRequestTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-invalid-request-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "invalid request strategy type - 'wrong-type'")
}

func TestAuthTokenNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-token-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "specified 'token' request strategy type but there are no its settings")
}

func TestAuthTokenEmptyValueThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-token-empty-value.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'token' request strategy validation, cause: there is no secret value")
}

func TestAuthJwtNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-jwt-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "specified 'jwt' request strategy type but there are no its settings")
}

func TestAuthJwtEmptyPublicKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-jwt-empty-public-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'jwt' request strategy validation, cause: there is no the public key path")
}

func TestAuthKeyNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-no-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, cause: no key id under index 0")
}

func TestAuthKeyDuplicateIdsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-duplicate-ids.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, key with id 'key1' already exists")
}

func TestAuthKeyDuplicateLocalKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-duplicate-local-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, local key 'secret-1' already exists")
}

func TestAuthKeyInvalidTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-invalid-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: invalid settings strategy type - 'wrong'")
}

func TestAuthKeyLocalNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-local-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: specified 'local' key management rule type but there are no its settings")
}

func TestAuthKeyLocalEmptyKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-local-empty-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: error during 'key1' key config validation, cause: 'key' field is empty")
}

func TestTlsEnabledNoCertificateThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-tls-enabled-no-cert.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "tls config validation error - the tls certificate can't be empty")
}

func TestTlsEnabledNoKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/server-config-tls-enabled-no-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "tls config validation error - the tls certificate key can't be empty")
}

func TestMethodsBanDurationNegativeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/methods-ban-negative.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: the method ban duration can't be less than 0")
}

func TestMethodsEnableDisableOverlapThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/methods-enable-disable-overlap.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: the method 'eth_getBlockByNumber' must not be enabled and disabled at the same time")
}

func TestRedisDefaults(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-valid-defaults.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	redisCfg := appCfg.CacheConfig.CacheConnectors[0].Redis
	expected := &config.RedisCacheConnectorConfig{
		Address:  "localhost:6379",
		DB:       lo.ToPtr(0),
		Timeouts: &config.RedisCacheConnectorTimeoutsConfig{ConnectTimeout: lo.ToPtr(500 * time.Millisecond), ReadTimeout: lo.ToPtr(200 * time.Millisecond), WriteTimeout: lo.ToPtr(200 * time.Millisecond)},
		Pool: &config.RedisCacheConnectorPoolConfig{
			Size:            10 * runtime.GOMAXPROCS(0),
			PoolTimeout:     lo.ToPtr(200*time.Millisecond + 1*time.Second),
			MinIdleConns:    0,
			MaxIdleConns:    0,
			MaxActiveConns:  0,
			ConnMaxIdleTime: lo.ToPtr(30 * time.Minute),
			ConnMaxLifeTime: lo.ToPtr(time.Duration(0)),
		},
	}

	assert.Equal(t, expected, redisCfg)
}

func TestRedisFullCustom(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-full-custom.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	redisCfg := appCfg.CacheConfig.CacheConnectors[0].Redis
	expected := &config.RedisCacheConnectorConfig{
		FullUrl:  "redis://user:pass@localhost:6380/2",
		Username: "user",
		Password: "pass",
		DB:       lo.ToPtr(2),
		Timeouts: &config.RedisCacheConnectorTimeoutsConfig{
			ConnectTimeout: lo.ToPtr(1 * time.Second),
			ReadTimeout:    lo.ToPtr(2 * time.Second),
			WriteTimeout:   lo.ToPtr(3 * time.Second),
		},
		Pool: &config.RedisCacheConnectorPoolConfig{
			Size:            64,
			PoolTimeout:     lo.ToPtr(5 * time.Second),
			MinIdleConns:    4,
			MaxIdleConns:    8,
			MaxActiveConns:  128,
			ConnMaxIdleTime: lo.ToPtr(10 * time.Minute),
			ConnMaxLifeTime: lo.ToPtr(1 * time.Hour),
		},
	}

	assert.Equal(t, expected, redisCfg)
}

func TestRedisMissingAddressThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-missing-address.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis3' validation, cause: either 'address' or 'full_url' must be specified")
}

func TestRedisNegativeReadTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-read-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis4' validation, cause: read timeout cannot be negative")
}

func TestRedisNegativeWriteTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-write-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis5' validation, cause: write timeout cannot be negative")
}

func TestRedisNegativeConnectTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-negative-connect-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis6' validation, cause: connect timeout cannot be negative")
}

func TestRedisPoolNegativeSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-size.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis7' validation, cause: pool size cannot be negative")
}

func TestRedisPoolNegativePoolTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-pool-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis8' validation, cause: pool timeout cannot be negative")
}

func TestRedisPoolMinGreaterMaxIdleThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-min-greater-max.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis9' validation, cause: pool min idle connections cannot be greater than pool max idle connections")
}

func TestRedisPoolNegativeMaxActiveThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-max-active.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis10' validation, cause: pool max connections cannot be negative")
}

func TestRedisPoolNegativeConnLifeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-conn-life.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis11' validation, cause: pool conn max life time cannot be negative")
}

func TestRedisPoolNegativeConnIdleThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-redis-pool-negative-conn-idle.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'redis12' validation, cause: pool conn max idle time cannot be negative")
}

func TestPostgresDefaults(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-valid-defaults.yaml")
	appCfg, err := config.NewAppConfig()
	require.NoError(t, err)

	pg := appCfg.CacheConfig.CacheConnectors[0].Postgres
	expected := &config.PostgresCacheConnectorConfig{
		Url:                   "postgres://user:pass@localhost:5432/db",
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
		Url:                   "postgres://user1:pass1@localhost:5432/db",
		ExpiredRemoveInterval: 1 * time.Minute,
		QueryTimeout:          lo.ToPtr(2 * time.Second),
		CacheTable:            "cache_custom",
	}

	assert.Equal(t, expected, pg)
}

func TestPostgresMissingUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/cache/cache-postgres-missing-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during cache connector 'pg3' validation, cause: 'url' must be specified")
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
