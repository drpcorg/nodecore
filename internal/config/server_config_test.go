package config_test

import (
	"net/netip"
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
		Port:            9095,
		MetricsPort:     9093,
		PprofPort:       6061,
		HealthPort:      9096,
		PyroscopeConfig: &config.PyroscopeConfig{},
		TlsConfig:       &config.TlsConfig{},
		GrpcAuthConfig: &config.GrpcAuthConfig{
			PublicKeyOwner: "drpc",
			SessionTTL:     24 * time.Hour,
		},
		Limits: &config.LimitsConfig{},
	}

	assert.Equal(t, &expected, appConfig.ServerConfig)
	// the routing headers are opt-in
	assert.False(t, appConfig.ServerConfig.RoutingHeaders)
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

func TestServerConfigLimits(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-limits.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, "2MB", appConfig.ServerConfig.Limits.MaxRequestSize)
	assert.Equal(t, int64(2*1024*1024), appConfig.ServerConfig.Limits.MaxRequestBytes())
	assert.Equal(t, 100, appConfig.ServerConfig.Limits.MaxBatchSize)
}

func TestServerConfigRoutingHeaders(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-routing-headers.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.True(t, appConfig.ServerConfig.RoutingHeaders)
}

// The limits are opt-in: a config that does not mention them limits nothing.
func TestServerConfigNoLimitsThenNothingIsLimited(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Zero(t, appConfig.ServerConfig.Limits.MaxRequestBytes())
	assert.Zero(t, appConfig.ServerConfig.Limits.MaxBatchSize)
}

func TestServerConfigLimitsRequestSizeUnits(t *testing.T) {
	tests := []struct {
		size     string
		expected int64
	}{
		{size: "512KB", expected: 512 * 1024},
		{size: "32MB", expected: 32 * 1024 * 1024},
		{size: "", expected: 0},
	}

	for _, test := range tests {
		t.Run(test.size, func(t *testing.T) {
			limits := &config.LimitsConfig{MaxRequestSize: test.size}

			assert.Equal(t, test.expected, limits.MaxRequestBytes())
		})
	}
}

func TestServerConfigLimitsWrongRequestSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-limits-wrong-request-size.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "limits validation error - max-request-size: size must be in KB or MB")
}

func TestServerConfigLimitsNegativeBatchSizeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/server/server-config-limits-negative-batch-size.yaml")
	_, err := config.NewAppConfig()

	assert.ErrorContains(t, err, "limits validation error - max-batch-size must not be negative")
}
