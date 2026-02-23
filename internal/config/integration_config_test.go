package config_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestDrpcIntegrationConfigNoUrlThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/integration/integration-config-no-url.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during drpc integration validation, cause - url cannot be empty")
}

func TestDrpcIntegrationConfigWrongRequestTimeoutThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/integration/integration-config-wrong-timeout.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during drpc integration validation, cause - request timeout cannot be less than 0")
}

func TestDrpcIntegrationConfigDefaultRequestTimeout(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/integration/default-request-timeout.yaml")
	appCfg, err := config.NewAppConfig()
	assert.NoError(t, err)

	expected := &config.DrpcIntegrationConfig{
		Url:            "http://localhost:9195",
		RequestTimeout: 10 * time.Second,
	}
	assert.Equal(t, expected, appCfg.IntegrationConfig.Drpc)
}
