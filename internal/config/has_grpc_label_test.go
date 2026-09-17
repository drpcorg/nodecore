package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSetHasGrpcLabel(t *testing.T) {
	grpcConnector := &ApiConnectorConfig{Type: "grpc", Url: "grpc://test.com:9090"}
	jsonRpcConnector := &ApiConnectorConfig{Type: "json-rpc", Url: "https://test.com"}

	t.Run("allocates the map when a grpc upstream has no labels", func(te *testing.T) {
		upstream := &Upstream{Connectors: []*ApiConnectorConfig{grpcConnector}}

		upstream.setHasGrpcLabel()

		assert.Equal(te, UpstreamLabels{hasGrpcLabel: "true"}, upstream.Labels)
	})

	t.Run("adds alongside unrelated labels", func(te *testing.T) {
		upstream := &Upstream{
			Labels:     UpstreamLabels{"provider": "hetzner"},
			Connectors: []*ApiConnectorConfig{jsonRpcConnector, grpcConnector},
		}

		upstream.setHasGrpcLabel()

		assert.Equal(te, UpstreamLabels{"provider": "hetzner", hasGrpcLabel: "true"}, upstream.Labels)
	})

	t.Run("does nothing without a grpc connector", func(te *testing.T) {
		upstream := &Upstream{Connectors: []*ApiConnectorConfig{jsonRpcConnector}}

		upstream.setHasGrpcLabel()

		assert.Nil(te, upstream.Labels)
	})

	t.Run("leaves an explicitly configured value alone", func(te *testing.T) {
		upstream := &Upstream{
			Labels:     UpstreamLabels{hasGrpcLabel: "false"},
			Connectors: []*ApiConnectorConfig{grpcConnector},
		}

		upstream.setHasGrpcLabel()

		assert.Equal(te, UpstreamLabels{hasGrpcLabel: "false"}, upstream.Labels)
	})
}

func TestSetDefaultsPublishesHasGrpcOnGrpcUpstreamsOnly(t *testing.T) {
	var upstreamConfig UpstreamConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
upstreams:
  - id: sui-upstream
    chain: sui
    connectors:
      - type: grpc
        url: grpc://test.com:9090
  - id: eth-upstream
    chain: ethereum
    labels:
      provider: hetzner
    connectors:
      - type: json-rpc
        url: https://test.com
`), &upstreamConfig))
	appConfig := &AppConfig{
		UpstreamConfig: &upstreamConfig,
		ServerConfig:   &ServerConfig{GrpcAuthConfig: &GrpcAuthConfig{Enabled: false}},
	}

	appConfig.setDefaults()

	require.Len(t, appConfig.UpstreamConfig.Upstreams, 2)
	assert.Equal(t, "true", appConfig.UpstreamConfig.Upstreams[0].Labels[hasGrpcLabel])
	_, set := appConfig.UpstreamConfig.Upstreams[1].Labels[hasGrpcLabel]
	assert.False(t, set, "an upstream without a grpc connector must not advertise grpc")
	assert.Equal(t, UpstreamLabels{"provider": "hetzner"}, appConfig.UpstreamConfig.Upstreams[1].Labels,
		"injection must not disturb configured labels")
}
