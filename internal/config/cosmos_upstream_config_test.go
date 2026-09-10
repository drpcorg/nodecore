package config_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cosmos node serves two APIs from one process - the CometBFT RPC on 26657
// and the SDK LCD on 1317 - so both connectors on one upstream is the normal
// deployment, and the tendermint one must win the head/internal-request role:
// it answers head, sync state, network id, version and earliest retained
// height from a single `status` call.
func TestCosmosUpstreamPrefersTendermintForHead(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/cosmos-tendermint-and-rest.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	require.Len(t, appConfig.UpstreamConfig.Upstreams, 1)
	upstream := appConfig.UpstreamConfig.Upstreams[0]

	assert.Equal(t, specs.TendermintConnector.String(), upstream.HeadConnector)
	assert.Equal(t, specs.TendermintConnector, upstream.GetBestConnector(config.DefaultMode))
	assert.ElementsMatch(t,
		[]specs.ApiConnectorType{specs.TendermintConnector, specs.RestConnector},
		upstream.GetApiConnectorTypes(),
	)
}

func TestGetBestConnectorWithTendermint(t *testing.T) {
	upstream := &config.Upstream{
		Connectors: []*config.ApiConnectorConfig{
			{Type: specs.RestConnector.String()},
			{Type: specs.TendermintConnector.String()},
		},
	}

	assert.Equal(t, specs.TendermintConnector, upstream.GetBestConnector(config.DefaultMode))
	assert.Equal(t, specs.RestConnector, upstream.GetBestConnector(config.StrictMode))
}

func TestTendermintIsValidHeadConnector(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/cosmos-tendermint-only.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	require.Len(t, appConfig.UpstreamConfig.Upstreams, 1)
	upstream := appConfig.UpstreamConfig.Upstreams[0]

	assert.Equal(t, specs.TendermintConnector.String(), upstream.HeadConnector)
	require.Len(t, upstream.Connectors, 1)
	assert.Equal(t, specs.TendermintConnector, upstream.Connectors[0].GetApiConnectorType())
}
