package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			ValidationInterval:        30 * time.Second,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
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
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			ValidationInterval:        30 * time.Second,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
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
		Options: &config.UpstreamOptions{
			InternalTimeout:           5 * time.Second,
			ValidationInterval:        30 * time.Second,
			DisableValidation:         lo.ToPtr(false),
			DisableSettingsValidation: lo.ToPtr(false),
			DisableChainValidation:    lo.ToPtr(false),
		},
	}

	assert.Equal(t, expected, appConfig.UpstreamConfig.Upstreams[0])
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

	assert.Equal(t, expected.UpstreamConfig.Upstreams[0], appConfig.UpstreamConfig.Upstreams[0])
	assert.Equal(t, expected.UpstreamConfig.ChainDefaults, appConfig.UpstreamConfig.ChainDefaults)
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

func TestOnionEndpointNoTorProxyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/tor-onion-no-proxy.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: tor proxy url is required for onion endpoints")
}

func TestInvalidConnectorUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/invalid-connector-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "invalid url for connector 'rest' -")
}

func TestOnionEndpointWithTorProxyThenSuccess(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/tor-onion-with-proxy.yaml")
	appConfig, err := config.NewAppConfig()
	assert.NoError(t, err)
	assert.NotNil(t, appConfig)
	assert.Equal(t, "localhost:9050", appConfig.ServerConfig.TorUrl)
	assert.Equal(t, 1, len(appConfig.UpstreamConfig.Upstreams))
	assert.Equal(t, "eth-upstream", appConfig.UpstreamConfig.Upstreams[0].Id)
	assert.Equal(t, 2, len(appConfig.UpstreamConfig.Upstreams[0].Connectors))
}

func TestUpstreamOptionsInvalidInternalTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/upstream-options-invalid-internal-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: internal timeout can't be less than 0")
}

func TestUpstreamOptionsInvalidValidationIntervalThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/upstream-options-invalid-validation-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: validation interval can't be less than 0")
}

func TestUpstreamOptionsDefaultsFromChain(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/upstream-options-defaults-from-chain.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	reqUp := appConfig.UpstreamConfig.Upstreams[0]
	require.NotNil(t, reqUp.Options)
	assert.Equal(t, 15*time.Second, reqUp.Options.InternalTimeout)
	assert.Equal(t, 1*time.Minute, reqUp.Options.ValidationInterval)
	assert.False(t, *reqUp.Options.DisableValidation)
	assert.False(t, *reqUp.Options.DisableSettingsValidation)
	assert.False(t, *reqUp.Options.DisableChainValidation)
}

func TestUpstreamOptionsOverrideFromUpstream(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/upstream-options-override-from-upstream.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	reqUp := appConfig.UpstreamConfig.Upstreams[0]
	require.NotNil(t, reqUp.Options)
	assert.Equal(t, 2*time.Second, reqUp.Options.InternalTimeout)
	assert.Equal(t, 45*time.Second, reqUp.Options.ValidationInterval)
}

func TestUpstreamOptionsDisableFlagsRead(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/upstream-options-disable-flags.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	reqUp := appConfig.UpstreamConfig.Upstreams[0]
	require.NotNil(t, reqUp.Options)
	assert.Equal(t, 5*time.Second, reqUp.Options.InternalTimeout)
	assert.Equal(t, 30*time.Second, reqUp.Options.ValidationInterval)
	assert.True(t, *reqUp.Options.DisableValidation)
	assert.True(t, *reqUp.Options.DisableSettingsValidation)
	assert.True(t, *reqUp.Options.DisableChainValidation)
}
