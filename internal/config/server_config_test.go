package config_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := config.ServerConfig{
		Port:                       9095,
		MetricsPort:                9093,
		PprofPort:                  6061,
		HealthPort:                 9096,
		GrpcUpstreamStatusInterval: 250 * time.Millisecond,
		PyroscopeConfig:            &config.PyroscopeConfig{},
		TlsConfig:                  &config.TlsConfig{},
		GrpcAuthConfig: &config.GrpcAuthConfig{
			PublicKeyOwner: "drpc",
			SessionTTL:     24 * time.Hour,
		},
	}

	assert.Equal(t, &expected, appConfig.ServerConfig)
}

func TestServerConfigTrustedProxiesParsedOnce(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-trusted-proxies.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.1"}, appConfig.ServerConfig.TrustedProxies)
	assert.Equal(
		t,
		[]netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("192.168.1.1/32"),
		},
		appConfig.ServerConfig.TrustedProxyPrefixes(),
	)
}

func TestServerConfigNoTrustedProxiesThenNoPrefixes(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Empty(t, appConfig.ServerConfig.TrustedProxyPrefixes())
}

func TestServerConfigWrongTrustedProxyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-wrong-trusted-proxies.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, `trusted-proxies validation error - invalid trusted proxy IP "not-an-ip"`)
}

func TestServerConfigEqualMetricsPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-equal-ports.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "metrics port 9095 is already in use")
}

func TestServerConfigEqualPprofPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-equal-pprof-ports.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "pprof port 8094 is already in use")
}

func TestServerConfigWrongServerPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-wrong-server-port.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "incorrect server port - -9095")
}

func TestServerConfigUpstreamStatusInterval(t *testing.T) {
	read := func(t *testing.T, interval string) (*config.AppConfig, error) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := "server:\n  grpc-upstream-status-interval: " + interval + "\n" +
			"upstream-config:\n  upstreams:\n    - id: eth\n      chain: ethereum\n      connectors:\n        - type: json-rpc\n          url: https://test.com\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		t.Setenv(config.ConfigPathVar, path)
		return config.NewAppConfig()
	}

	for interval, expected := range map[string]time.Duration{"0s": config.DefaultGrpcUpstreamStatusInterval, "5ms": 5 * time.Millisecond, "1s": time.Second} {
		appConfig, err := read(t, interval)
		require.NoError(t, err, interval)
		assert.Equal(t, expected, appConfig.ServerConfig.GrpcUpstreamStatusInterval, interval)
	}
	for _, interval := range []string{"-1s", "1ns", "4ms", "1001ms"} {
		_, err := read(t, interval)
		assert.ErrorContains(t, err, "incorrect grpc upstream status interval - ", interval)
		assert.ErrorContains(t, err, "must be within [5ms, 1s]", interval)
	}
}

func TestServerConfigWrongMetricsPortThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-wrong-metrics-port.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "incorrect metrics port - -23555")
}

func TestPyroConfigNoUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-pyro-no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, url must be specified`)
}

func TestPyroConfigNoUsernameThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-pyro-no-username.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, username must be specified`)
}

func TestPyroConfigNoPasswordThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-pyro-no-password.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `pyroscope is enabled, password must be specified`)
}

func TestTlsEnabledNoCertificateThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-tls-enabled-no-cert.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "tls config validation error - the tls certificate can't be empty")
}

func TestTlsEnabledNoKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-tls-enabled-no-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "tls config validation error - the tls certificate key can't be empty")
}
