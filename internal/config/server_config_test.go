package config_test

import (
	"testing"

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
		PyroscopeConfig: &config.PyroscopeConfig{},
		TlsConfig:       &config.TlsConfig{},
	}

	assert.Equal(t, &expected, appConfig.ServerConfig)
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
