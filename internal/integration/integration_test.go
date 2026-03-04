package integration_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/stretchr/testify/assert"
)

func TestIntegrationResolverWithDrpcAndLocal(t *testing.T) {
	cfg := &config.IntegrationConfig{
		Drpc: &config.DrpcIntegrationConfig{Url: "http://localhost:8080"},
	}
	resolver := integration.NewIntegrationResolver(cfg)

	assert.NotNil(t, resolver.GetIntegration(integration.Drpc))
	assert.NotNil(t, resolver.GetIntegration(integration.Local))
}

func TestIntegrationResolverLocalAlways(t *testing.T) {
	resolver := integration.NewIntegrationResolver(nil)

	assert.NotNil(t, resolver.GetIntegration(integration.Local))
}
