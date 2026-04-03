package stats

import (
	"context"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/protocol"
	"time"
)

type StatsOutboxStorer interface {
	OutboxStore(ctx context.Context, key string, value []byte, ttl time.Duration) error
	OutboxRemove(ctx context.Context, key string) error
	OutboxList(ctx context.Context, cursor, limit int64) ([]map[string][]byte, error)
}

type StatsService interface {
	Start(_ StatsOutboxStorer)
	Stop(ctx context.Context) error
	AddRequestResults(requestResults []protocol.RequestResult)
}

type noopStatsService struct {
}

func (n *noopStatsService) Start(_ StatsOutboxStorer) {
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
