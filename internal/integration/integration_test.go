package integration_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/stretchr/testify/assert"
)

func TestIntegrationResolverWithDrpc(t *testing.T) {
	cfg := &config.IntegrationConfig{
		Drpc: &config.DrpcIntegrationConfig{Url: "http://localhost:8080"},
	}
	resolver := integration.NewIntegrationResolver(cfg)

	assert.NotNil(t, resolver.GetIntegration("drpc"))
}
