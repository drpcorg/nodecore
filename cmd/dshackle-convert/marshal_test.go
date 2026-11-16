package main

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The converter writes nodecore's own config structs. Their yaml tags carry omitempty, so a
// field the converter did not set must not show up in the file.
func TestMarshalConfigWritesOnlyWhatIsSet(t *testing.T) {
	cfg := &config.AppConfig{
		ServerConfig: &config.ServerConfig{GrpcPort: 2449},
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              config.StrictProviderMode,
			BalancingStrategy: config.BaseBalancingStrategy,
			Upstreams: []*config.Upstream{{
				Id:         "a",
				ChainName:  "ethereum",
				Connectors: []*config.ApiConnectorConfig{{Type: "json-rpc", Url: "http://n:1"}},
			}},
		},
	}
	data, err := marshalConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, `server:
  grpc-port: 2449
upstream-config:
  upstreams:
    - id: a
      chain: ethereum
      connectors:
        - type: json-rpc
          url: http://n:1
  balancing-strategy: base
  mode: strict-provider
`, string(data))
}

func TestMarshalConfigKeepsWhatIsSet(t *testing.T) {
	secret := `Basic p#ss: w"ord`
	cfg := &config.AppConfig{
		ServerConfig: &config.ServerConfig{
			GrpcPort: 2449, MetricsPort: 8081,
			// a disabled section must still say so
			TlsConfig:      &config.TlsConfig{Certificate: "/c.crt"},
			GrpcAuthConfig: &config.GrpcAuthConfig{PublicKeyOwner: "drpc"},
		},
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              config.StrictProviderMode,
			BalancingStrategy: config.BaseBalancingStrategy,
			ChainDefaults: map[string]*config.ChainDefaults{
				"ethereum": {Options: &chains.Options{MinPeers: 10}},
			},
			Upstreams: []*config.Upstream{{
				Id:        "eth-1",
				ChainName: "ethereum",
				Labels:    config.UpstreamLabels{"archive": "true", "version": "1.10"},
				Connectors: []*config.ApiConnectorConfig{{
					Type: "json-rpc", Url: "http://localhost:8545", Headers: map[string]string{"Authorization": secret},
				}},
				Options:     &chains.Options{ValidationInterval: 45 * time.Second, DisableChainValidation: new(true), ValidatePeers: new(false)},
				Methods:     &config.MethodsConfig{EnableMethods: []string{"trace"}},
				LowerBounds: config.UpstreamLowerBounds{"state": 100},
			}},
		},
	}
	data, err := marshalConfig(cfg)
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "  tls:\n    enabled: false\n    certificate: /c.crt\n")
	assert.Contains(t, text, "  grpc-auth:\n    enabled: false\n    public-key-owner: drpc\n")
	assert.Contains(t, text, "validation-interval: 45s\n")
	assert.Contains(t, text, "validate-peers: false\n")
	for _, absent := range []string{"key:", "provider-private-key-path", "session-ttl", "health-port", "head-connector", "poll-interval", "ban-duration", "call-limit-size", "internal-timeout", " port:"} {
		assert.NotContains(t, text, absent)
	}

	parsed := strictDecode(t, data)
	up := parsed.UpstreamConfig.Upstreams[0]
	assert.Equal(t, secret, up.Connectors[0].Headers["Authorization"])
	assert.Equal(t, "1.10", up.Labels["version"])
	assert.Equal(t, 45*time.Second, up.Options.ValidationInterval)
	assert.Equal(t, int64(100), up.LowerBounds["state"])
	assert.Equal(t, int64(10), parsed.UpstreamConfig.ChainDefaults["ethereum"].Options.MinPeers)
}
