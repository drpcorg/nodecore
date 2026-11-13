package config

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
)

func TestUpstreamModeIsStrict(t *testing.T) {
	assert.False(t, DefaultMode.IsStrict())
	assert.True(t, StrictMode.IsStrict())
	assert.True(t, StrictProviderMode.IsStrict())
	assert.False(t, UpstreamMode("wrong").IsStrict())
}

func TestUpstreamModeValidate(t *testing.T) {
	assert.NoError(t, DefaultMode.Validate())
	assert.NoError(t, StrictMode.Validate())
	assert.NoError(t, StrictProviderMode.Validate())
	assert.EqualError(t, UpstreamMode("wrong").Validate(), "invalid upstream mode: wrong")
}

func TestStrictProviderModeMatchesStrict(t *testing.T) {
	cfg := &UpstreamConfig{Mode: StrictProviderMode}

	dispatch := cfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.True(t, *dispatch.Broadcast)
	assert.True(t, *dispatch.MaximumValue)
	assert.True(t, *dispatch.NotNull)

	assert.True(t, cfg.ValidateLagFor(chains.ETHEREUM.String()))

	upstream := &Upstream{
		Connectors: []*ApiConnectorConfig{
			{Type: specs.JsonRpcConnector.String()},
			{Type: specs.WebsocketConnector.String()},
		},
	}
	assert.Equal(t, specs.WebsocketConnector, upstream.GetBestConnector(StrictProviderMode))

	assert.Equal(
		t,
		chains.GetChain("ethereum").Settings.ExpectedBlockTime,
		getDefaultPollInterval("ethereum", StrictProviderMode),
	)
}

func TestSetOptionsDefaultsStrictProviderMatchesStrict(t *testing.T) {
	strictOptions := &chains.Options{}
	providerOptions := &chains.Options{}

	setOptionsDefaults(strictOptions, nil, chainSettings(nil), StrictMode)
	setOptionsDefaults(providerOptions, nil, chainSettings(nil), StrictProviderMode)

	assert.Equal(t, *strictOptions.DisableLowerBoundsDetection, *providerOptions.DisableLowerBoundsDetection)
	assert.Equal(t, *strictOptions.DisableSafeBlockDetection, *providerOptions.DisableSafeBlockDetection)
	assert.Equal(t, *strictOptions.DisableLabelsDetection, *providerOptions.DisableLabelsDetection)
	assert.Equal(t, *strictOptions.DisableMethodsDetection, *providerOptions.DisableMethodsDetection)
	assert.Equal(t, *strictOptions.DisableLogIndexValidation, *providerOptions.DisableLogIndexValidation)
	assert.Equal(t, *strictOptions.DisableErigonBugValidation, *providerOptions.DisableErigonBugValidation)
	assert.Equal(t, *strictOptions.DisableLivenessSubscriptionValidation, *providerOptions.DisableLivenessSubscriptionValidation)
	assert.Equal(t, *strictOptions.ValidateSyncing, *providerOptions.ValidateSyncing)
	assert.Equal(t, *strictOptions.ValidatePeers, *providerOptions.ValidatePeers)
	assert.Equal(t, *strictOptions.ValidateCallLimit, *providerOptions.ValidateCallLimit)

	assert.False(t, *providerOptions.DisableLabelsDetection)
	assert.True(t, *providerOptions.ValidateCallLimit)
}
