package config_test

import (
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNoConfigFileThenError(t *testing.T) {
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "open ./dShaltie.yml: no such file or directory")
}

func TestReadFullConfig(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/valid-full-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
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
					Connectors: []*config.ConnectorConfig{
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
				},
				{
					Id:            "another",
					HeadConnector: config.Rest,
					PollInterval:  1 * time.Minute,
					ChainName:     "polygon",
					Connectors: []*config.ConnectorConfig{
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
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/not-supported-chain-defaults.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during chain defaults validation, cause: not supported chain no-chain")
}

func TestNoUpstreamIdThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/no-upstream-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream validation, cause: no upstream id under index 1")
}

func TestDuplicateIdsThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/duplicate-ids.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream validation, cause: upstream with id another already exists")
}

func TestNoSupportedUpstreamChainThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/not-supported-up-chain.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: not supported chain wrong")
}

func TestInvalidHeadConnectorThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/invalid-head-connector.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: invalid head connector")
}

func TestNoConnectorsThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/no-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: there must be at least one upstream connector")
}

func TestWrongHeadConnectorError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/wrong-head-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "there is no json-rpc connector for head")
}

func TestDuplicateConnectorsThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/duplicate-connectors.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: there can be only one connector of type json-rpc")
}

func TestInvalidConnectorTypeThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/invalid-connector-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: invalid connector type - wrong-type")
}

func TestNoConnectorUrlThenError(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream eth-upstream validation, cause: url must be specified for connector rest")
}

func TestSetDefaultPollInterval(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/default-poll-interval.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			Upstreams: []*config.Upstream{
				{
					Id:            "eth-upstream",
					HeadConnector: config.JsonRpc,
					PollInterval:  1 * time.Minute,
					ChainName:     "ethereum",
					Connectors: []*config.ConnectorConfig{
						{
							Type: config.JsonRpc,
							Url:  "https://test.com",
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}

func TestSetDefaultJsonRpcHeadConnector(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/default-head-connector.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			Upstreams: []*config.Upstream{
				{
					Id:            "eth-upstream",
					HeadConnector: config.JsonRpc,
					PollInterval:  1 * time.Minute,
					ChainName:     "ethereum",
					Connectors: []*config.ConnectorConfig{
						{
							Type: config.JsonRpc,
							Url:  "https://test.com",
						},
						{
							Type: config.Rest,
							Url:  "https://test.com",
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}

func TestSetDefaultRestHeadConnector(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/default-rest-head-connector.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			Upstreams: []*config.Upstream{
				{
					Id:            "eth-upstream",
					HeadConnector: config.Rest,
					PollInterval:  1 * time.Minute,
					ChainName:     "ethereum",
					Connectors: []*config.ConnectorConfig{
						{
							Type: config.Ws,
							Url:  "https://test.com",
						},
						{
							Type: config.Rest,
							Url:  "https://test.com",
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}

func TestSetChainsDefault(t *testing.T) {
	t.Setenv("DSHALTIE_CONFIG_PATH", "configs/default-from-chains.yaml")
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
					Id:            "eth-upstream",
					HeadConnector: config.JsonRpc,
					PollInterval:  10 * time.Minute,
					ChainName:     "ethereum",
					Connectors: []*config.ConnectorConfig{
						{
							Type: config.JsonRpc,
							Url:  "https://test.com",
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expected, appConfig)
}
