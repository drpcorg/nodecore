package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGrpcAuthConfigDisabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *GrpcAuthConfig
		want bool
	}{
		{
			name: "no config at all",
			cfg:  nil,
			want: true,
		},
		{
			name: "explicitly switched off",
			cfg:  &GrpcAuthConfig{Enabled: false, ProviderPrivateKeyPath: "/tmp/private.pem"},
			want: true,
		},
		{
			name: "enabled but no key to sign with",
			cfg:  &GrpcAuthConfig{Enabled: true},
			want: true,
		},
		{
			name: "enabled with a key",
			cfg:  &GrpcAuthConfig{Enabled: true, ProviderPrivateKeyPath: "/tmp/private.pem"},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.want, test.cfg.Disabled())
		})
	}
}

func TestSetSecureSignedLabel(t *testing.T) {
	t.Run("allocates the map when the upstream has no labels", func(te *testing.T) {
		upstream := &Upstream{}

		upstream.setSecureSignedLabel()

		assert.Equal(te, UpstreamLabels{SecureSignedLabel: "true"}, upstream.Labels)
	})

	t.Run("adds alongside unrelated labels", func(te *testing.T) {
		upstream := &Upstream{Labels: UpstreamLabels{"provider": "hetzner"}}

		upstream.setSecureSignedLabel()

		assert.Equal(te, UpstreamLabels{"provider": "hetzner", SecureSignedLabel: "true"}, upstream.Labels)
	})

	// An operator can opt a single upstream out - dshackle skips injection the same way.
	t.Run("leaves an explicitly configured value alone", func(te *testing.T) {
		upstream := &Upstream{Labels: UpstreamLabels{SecureSignedLabel: "false"}}

		upstream.setSecureSignedLabel()

		assert.Equal(te, UpstreamLabels{SecureSignedLabel: "false"}, upstream.Labels)
	})
}

func secureSignedTestConfig(t *testing.T, grpcAuth *GrpcAuthConfig) *AppConfig {
	t.Helper()

	var upstreamConfig UpstreamConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    connectors:
      - type: json-rpc
        url: https://test.com
  - id: poly-upstream
    chain: polygon
    labels:
      provider: hetzner
    connectors:
      - type: json-rpc
        url: https://test2.com
`), &upstreamConfig))

	return &AppConfig{
		UpstreamConfig: &upstreamConfig,
		ServerConfig:   &ServerConfig{GrpcAuthConfig: grpcAuth},
	}
}

func TestSetDefaultsPublishesSecureSignedOnEveryUpstream(t *testing.T) {
	appConfig := secureSignedTestConfig(t, &GrpcAuthConfig{
		Enabled:                true,
		ProviderPrivateKeyPath: "/tmp/private.pem",
	})

	appConfig.setDefaults()

	require.Len(t, appConfig.UpstreamConfig.Upstreams, 2)
	assert.Equal(t, "true", appConfig.UpstreamConfig.Upstreams[0].Labels[SecureSignedLabel])
	assert.Equal(t, "true", appConfig.UpstreamConfig.Upstreams[1].Labels[SecureSignedLabel])
	assert.Equal(t, "hetzner", appConfig.UpstreamConfig.Upstreams[1].Labels["provider"],
		"injection must not disturb configured labels")
}

func TestSetDefaultsSkipsSecureSignedWhenSigningIsUnavailable(t *testing.T) {
	tests := map[string]*GrpcAuthConfig{
		"no grpc auth config": nil,
		"switched off":        {Enabled: false, ProviderPrivateKeyPath: "/tmp/private.pem"},
		"no private key":      {Enabled: true},
	}

	for name, grpcAuth := range tests {
		t.Run(name, func(te *testing.T) {
			appConfig := secureSignedTestConfig(te, grpcAuth)

			appConfig.setDefaults()

			for _, upstream := range appConfig.UpstreamConfig.Upstreams {
				_, set := upstream.Labels[SecureSignedLabel]
				assert.False(te, set, "upstream '%s' must not advertise signing", upstream.Id)
			}
		})
	}
}
