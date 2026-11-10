package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompatibleClientsStrictModeEnabledByDefault(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-strict-default.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	section := appConfig.UpstreamConfig.CompatibleClients

	assert.True(t, section.IsEnabled())
	assert.Equal(t, compatible_clients.DefaultUrl, section.Url)
	assert.Equal(t, 10*time.Minute, section.RefreshInterval)
}

func TestCompatibleClientsDefaultModeDisabledByDefault(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-default-mode.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	section := appConfig.UpstreamConfig.CompatibleClients

	assert.False(t, section.IsEnabled())
	assert.Equal(t, compatible_clients.DefaultUrl, section.Url)
	assert.Equal(t, 10*time.Minute, section.RefreshInterval)
}

func TestCompatibleClientsExplicitValues(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-explicit.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	section := appConfig.UpstreamConfig.CompatibleClients

	assert.True(t, section.IsEnabled())
	assert.Equal(t, "file:///tmp/x.yaml", section.Url)
	assert.Equal(t, time.Minute, section.RefreshInterval)
}

func TestCompatibleClientsDisabledIgnoresUrl(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-disabled-no-url.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.False(t, appConfig.UpstreamConfig.CompatibleClients.IsEnabled())
}

func TestCompatibleClientsEmptyUrlTakesDefault(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-enabled-no-url.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, compatible_clients.DefaultUrl, appConfig.UpstreamConfig.CompatibleClients.Url)
}

func TestCompatibleClientsRejectsNonPositiveInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/upstreams/compatible-clients-bad-interval.yaml")
	_, err := config.NewAppConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh-interval")
}
