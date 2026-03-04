package config_test

import (
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
		AppStorages: []config.AppStorageConfig{
			{
				Name: "redis-storage-1",
				Redis: &config.RedisStorageConfig{
					FullUrl:  "redis://localhost:6379/0",
					Address:  "localhost:6379",
					Username: "username",
					Password: "password",
					DB:       lo.ToPtr(2),
					Timeouts: &config.RedisStorageTimeoutsConfig{
						ConnectTimeout: lo.ToPtr(1 * time.Second),
						ReadTimeout:    lo.ToPtr(2 * time.Second),
						WriteTimeout:   lo.ToPtr(3 * time.Second),
					},
					Pool: &config.RedisStoragePoolConfig{
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
				Name: "postgres-storage-1",
				Postgres: &config.PostgresStorageConfig{
					Url: "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
				},
			},
		},
		IntegrationConfig: &config.IntegrationConfig{
			Drpc: &config.DrpcIntegrationConfig{
				Url:            "http://localhost:9090",
				RequestTimeout: 35 * time.Second,
			},
		},
		StatsConfig: &config.StatsConfig{
			Enabled:       true,
			Type:          config.Drpc,
			FlushInterval: 5 * time.Minute,
		},
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
				{
					Id:   "drpc-super-key",
					Type: config.Drpc,
					DrpcKeyConfig: &config.DrpcKeyConfig{
						Owner: &config.DrpcOwnerConfig{
							Id:       "id",
							ApiToken: "apiToken",
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
						StorageName: "redis-storage-1",
					},
				},
				{
					Id:     "postgresql-connector",
					Driver: config.Postgres,
					Postgres: &config.PostgresCacheConnectorConfig{
						StorageName:           "postgres-storage-1",
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
					RateLimit: &config.RateLimiterConfig{
						Rules: []config.RateLimitRule{
							{
								Method:   "eth_getBlockByNumber",
								Requests: 100,
								Period:   time.Second,
							},
							{
								Pattern:  "eth_getBlockByNumber|eth_getBlockByHash",
								Requests: 50,
								Period:   time.Second,
							},
							{
								Pattern:  "trace_.*",
								Requests: 5,
								Period:   2 * time.Minute,
							},
						},
					},
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
					Options: &config.UpstreamOptions{
						InternalTimeout:           5 * time.Second,
						ValidationInterval:        30 * time.Second,
						DisableValidation:         lo.ToPtr(false),
						DisableSettingsValidation: lo.ToPtr(false),
						DisableChainValidation:    lo.ToPtr(false),
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

					RateLimit: &config.RateLimiterConfig{Rules: nil},
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
					Options: &config.UpstreamOptions{
						InternalTimeout:           5 * time.Second,
						ValidationInterval:        30 * time.Second,
						DisableValidation:         lo.ToPtr(false),
						DisableSettingsValidation: lo.ToPtr(false),
						DisableChainValidation:    lo.ToPtr(false),
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}
