package integration

import (
	"github.com/drpcorg/nodecore/internal/config"
)

type IntegrationClient interface {
	InitKeys(cfg config.IntegrationKeyConfig) (chan KeyEvent, error)
	Type() IntegrationType
}

type IntegrationType string

const (
	Drpc IntegrationType = "drpc"
)

type IntegrationResolver struct {
	integrations map[IntegrationType]IntegrationClient
}

func NewIntegrationResolver(cfg *config.IntegrationConfig) *IntegrationResolver {
	integrations := make(map[IntegrationType]IntegrationClient)
	resolver := &IntegrationResolver{
		integrations: integrations,
	}

	if cfg == nil {
		return resolver
	}

	if cfg.Drpc != nil {
		integrations[Drpc] = NewDrpcIntegrationClient(cfg.Drpc)
	}

	return resolver
}

func NewNewIntegrationResolverWithClients(clients map[IntegrationType]IntegrationClient) *IntegrationResolver {
	return &IntegrationResolver{
		integrations: clients,
	}
}

func (i *IntegrationResolver) GetIntegration(integrationType IntegrationType) IntegrationClient {
	return i.integrations[integrationType]
}
