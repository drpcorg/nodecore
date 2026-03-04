package integration_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/integration/local"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/stretchr/testify/assert"
)

func TestLocalIntegrationType(t *testing.T) {
	localIntegration := integration.NewLocalIntegration()

	assert.Equal(t, integration.Local, localIntegration.Type())
}

func TestLocalIntegrationNotLocalKeyCfgThenErr(t *testing.T) {
	localIntegration := integration.NewLocalIntegration()

	events, err := localIntegration.InitKeys("id", &config.ExternalKeyConfig{})

	assert.Nil(t, events)
	assert.ErrorContains(t, err, "local init keys expects local key config")
}

func TestLocalIntegrationInitKeys(t *testing.T) {
	localIntegration := integration.NewLocalIntegration()
	cfg := &config.LocalKeyConfig{
		Key: "secret-key",
		KeySettingsConfig: &config.KeySettingsConfig{
			AllowedIps: []string{"1.1.1.1"},
			Methods: &config.AuthMethods{
				Allowed:   []string{"method"},
				Forbidden: []string{"method2"},
			},
			AuthContracts: &config.AuthContracts{
				Allowed: []string{"contract"},
			},
			CorsOrigins: []string{"http://localhost:8080"},
		},
	}

	expectedKey := local.NewLocalKey("id", cfg)

	events, err := localIntegration.InitKeys("id", cfg)
	assert.NoError(t, err)

	key := <-events
	assert.Equal(t, expectedKey, key.(*keydata.UpdatedKeyEvent).NewKey)
}
