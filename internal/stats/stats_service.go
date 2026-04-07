package stats

import (
	"context"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/outbox"
	"github.com/drpcorg/nodecore/internal/protocol"
)

type StatsService interface {
	Start(_ outbox.Storer)
	Stop(ctx context.Context) error
	AddRequestResults(requestResults []protocol.RequestResult)
}

type noopStatsService struct {
}

func (n *noopStatsService) Start(_ outbox.Storer) {
	// noop
}

func (n *noopStatsService) Stop(_ context.Context) error {
	return nil
}

func (n *noopStatsService) AddRequestResults(_ []protocol.RequestResult) {
	// noop
}

var _ StatsService = (*noopStatsService)(nil)

func NewStatsService(
	ctx context.Context,
	statsConfig *config.StatsConfig,
	integrationResolver *integration.IntegrationResolver,
) StatsService {
	if statsConfig == nil || !statsConfig.Enabled {
		return &noopStatsService{}
	}
	return NewBaseStatsService(ctx, statsConfig, integrationResolver)
}
