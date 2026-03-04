package integration

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type IntegrationClient interface {
	InitKeys(id string, cfg config.IntegrationKeyConfig) (chan keydata.KeyEvent, error)
	GetStatsSchema() []statsdata.StatsDims
	ProcessStatsData(aggregatedData *utils.CMap[statsdata.StatsKey, statsdata.StatsData])
	Type() IntegrationType
}

type IntegrationType string

const (
	Drpc  IntegrationType = "drpc"
	Local IntegrationType = "local"
)

type IntegrationResolver struct {
	integrations map[IntegrationType]IntegrationClient
}

func NewIntegrationResolver(cfg *config.IntegrationConfig) *IntegrationResolver {
	integrations := make(map[IntegrationType]IntegrationClient)
	resolver := &IntegrationResolver{
		integrations: integrations,
	}
	// to handle local logic like local keys, local stats, etc...
	integrations[Local] = NewLocalIntegration()

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

func GetIntegrationType(configType config.IntegrationType) IntegrationType {
	switch configType {
	case config.Local:
		return Local
	case config.Drpc:
		return Drpc
	default:
		panic(fmt.Sprintf("unknown integration type - %s", configType))
	}
}
