package integration

import (
	"context"
	"errors"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/google/go-cmp/cmp"
	"github.com/rs/zerolog/log"
)

type DrpcIntegrationClient struct {
	ctx          context.Context
	connector    drpc.DrpcHttpConnector
	ownerKeys    *utils.CMap[string, map[string]*drpc.DrpcKey]
	pollInterval time.Duration
}

func (d *DrpcIntegrationClient) ProcessStatsData(_ *utils.CMap[statsdata.StatsKey, statsdata.StatsData]) {
	// noop
}

func (d *DrpcIntegrationClient) GetStatsSchema() []statsdata.StatsDims {
	// could be hardcoded at first
	// but in the future the scheme could be fetched from the drpc backend
	return nil
}

func (d *DrpcIntegrationClient) Type() IntegrationType {
	return Drpc
}

func (d *DrpcIntegrationClient) InitKeys(_ string, cfg config.IntegrationKeyConfig) (chan keydata.KeyEvent, error) {
	drpcKeyCfg, ok := cfg.(*config.DrpcKeyConfig)
	if !ok {
		return nil, errors.New("drpc init keys expects drpc key config")
	}

	if drpcKeyCfg.Owner == nil {
		return nil, errors.New("there must be drpc owner config to init drpc keys")
	}

	keyEvents := make(chan keydata.KeyEvent, 100)
	go d.pollKeys(drpcKeyCfg.Owner.Id, drpcKeyCfg.Owner.ApiToken, keyEvents)

	return keyEvents, nil
}

func (d *DrpcIntegrationClient) pollKeys(ownerId, apiToken string, keyEvents chan keydata.KeyEvent) {
	d.processKeys(ownerId, apiToken, keyEvents)
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-time.After(d.pollInterval):
			d.processKeys(ownerId, apiToken, keyEvents)
		}
	}
}

func (d *DrpcIntegrationClient) processKeys(ownerId, apiToken string, keyEvents chan keydata.KeyEvent) {
	currentKeys, _ := d.ownerKeys.LoadOrStore(ownerId, map[string]*drpc.DrpcKey{})
	ownerKeys, err := d.getOwnerKeys(ownerId, apiToken)
	if err != nil {
		log.Warn().Err(err).Msgf("error polling drpc keys for owner '%s'", ownerId)
		return
	}

	newKeys := mapset.NewThreadUnsafeSet[string]()
	for _, key := range ownerKeys {
		newKeys.Add(key.GetKeyValue())
		currentKey, ok := currentKeys[key.GetKeyValue()]

		if !ok || !cmp.Equal(currentKey, key) {
			keyEvents <- keydata.NewUpdatedKeyEvent(key)
			currentKeys[key.GetKeyValue()] = key
		}
	}

	for apiKey, key := range currentKeys {
		if !newKeys.Contains(apiKey) {
			keyEvents <- keydata.NewRemovedKeyEvent(key)
			delete(currentKeys, apiKey)
		}
	}
}

func (d *DrpcIntegrationClient) getOwnerKeys(ownerId, apiToken string) ([]*drpc.DrpcKey, error) {
	err := d.connector.OwnerExists(ownerId, apiToken)
	if err != nil {
		return nil, err
	}
	drpcKeys, err := d.connector.LoadOwnerKeys(ownerId, apiToken)
	if err != nil {
		return nil, err
	}
	return drpcKeys, nil
}

func NewDrpcIntegrationClientWithConnector(ctx context.Context, connector drpc.DrpcHttpConnector, pollInterval time.Duration) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:          ctx,
		connector:    connector,
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: pollInterval,
	}
}

func NewDrpcIntegrationClient(
	drpcIntegration *config.DrpcIntegrationConfig,
) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:          context.Background(),
		connector:    drpc.NewSimpleDrpcHttpConnector(drpcIntegration),
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: 1 * time.Minute,
	}
}

var _ IntegrationClient = (*DrpcIntegrationClient)(nil)
