package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatsConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/stats/stats-config.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := config.StatsConfig{
		Enabled:       true,
		Type:          config.Drpc,
		FlushInterval: 10 * time.Minute,
	}

	assert.Equal(t, &expected, appConfig.StatsConfig)
}

func TestStatsConfigDefaultFlushInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/stats/stats-config-no-flush-interval.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)

	expected := config.StatsConfig{
		Enabled:       true,
		Type:          config.Drpc,
		FlushInterval: 3 * time.Minute,
	}

	assert.Equal(t, &expected, appConfig.StatsConfig)
}

func TestStatsConfigInvalidType(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/stats/stats-config-invalid-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `stats type validation error - invalid settings strategy type - 'invalid-type!'`)
}

func TestStatsConfigInvalidFlushInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/stats/stats-config-invalid-flush-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, `stats flush-interval must be greater than 3 minutes`)
}
