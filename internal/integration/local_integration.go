package integration

import (
	"errors"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/local"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type LocalIntegration struct {
}

func (l *LocalIntegration) ProcessStatsData(_ *utils.CMap[statsdata.StatsKey, statsdata.StatsData]) {
	// noop
}

func (l *LocalIntegration) GetStatsSchema() []statsdata.StatsDims {
	return []statsdata.StatsDims{statsdata.Chain, statsdata.UpstreamId, statsdata.Method, statsdata.ReqKind, statsdata.RespKind}
}

func (l *LocalIntegration) InitKeys(id string, cfg config.IntegrationKeyConfig) (chan keydata.KeyEvent, error) {
	localKeyCfg, ok := cfg.(*config.LocalKeyConfig)
	if !ok {
		return nil, errors.New("local init keys expects local key config")
	}

	keyEvents := make(chan keydata.KeyEvent, 1)
	localKey := local.NewLocalKey(id, localKeyCfg)
	keyEvents <- keydata.NewUpdatedKeyEvent(localKey)

	return keyEvents, nil
}

func (l *LocalIntegration) Type() IntegrationType {
	return Local
}

func NewLocalIntegration() *LocalIntegration {
	return &LocalIntegration{}
}

var _ IntegrationClient = (*LocalIntegration)(nil)
