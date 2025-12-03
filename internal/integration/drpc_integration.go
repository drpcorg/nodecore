package integration

import (
	"errors"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/google/go-cmp/cmp"
	"github.com/rs/zerolog/log"
)

type DrpcIntegrationClient struct {
	connector    drpc.DrpcHttpConnector
	ownerKeys    *utils.CMap[string, map[string]*drpc.DrpcKey]
	pollInterval time.Duration
}

func (d *DrpcIntegrationClient) Type() IntegrationType {
	return Drpc
}

func (d *DrpcIntegrationClient) InitKeys(cfg config.IntegrationKeyConfig) (*InitKeysData, error) {
	drpcKeyCfg, ok := cfg.(*config.DrpcKeyConfig)
	if !ok {
		return nil, errors.New("drpc init keys expects drpc key config")
	}

	if drpcKeyCfg.Owner == nil {
		return nil, errors.New("there must be drpc owner config to init drpc keys")
	}

	drpcKeys, err := d.getOwnerKeys(drpcKeyCfg.Owner.Id, drpcKeyCfg.Owner.ApiToken)
	if err != nil {
		return nil, err
	}

	keys := make([]keymanagement.Key, 0, len(drpcKeys))
	keyMap := map[string]*drpc.DrpcKey{}
	for _, key := range drpcKeys {
		keyMap[key.GetKeyValue()] = key
		keys = append(keys, key)
	}
	d.ownerKeys.Store(drpcKeyCfg.Owner.Id, keyMap)

	keyEvents := make(chan KeyEvent, 100)
	go d.pollKeys(drpcKeyCfg.Owner.Id, drpcKeyCfg.Owner.ApiToken, keyEvents)

	return NewInitKeysData(keys, keyEvents), nil
}

func (d *DrpcIntegrationClient) pollKeys(ownerId, apiToken string, keyEvents chan KeyEvent) {
	for {
		time.Sleep(d.pollInterval)
		currentKeys, _ := d.ownerKeys.Load(ownerId)
		ownerKeys, err := d.getOwnerKeys(ownerId, apiToken)
		if err != nil {
			log.Warn().Err(err).Msgf("error polling drpc keys for owner '%s'", ownerId)
			continue
		}

		newKeys := mapset.NewThreadUnsafeSet[string]()
		for _, key := range ownerKeys {
			newKeys.Add(key.GetKeyValue())
			currentKey, ok := currentKeys[key.GetKeyValue()]

			if !ok || !cmp.Equal(currentKey, key) {
				keyEvents <- NewUpdatedKeyEvent(key)
				currentKeys[key.GetKeyValue()] = key
			}
		}

		for apiKey, key := range currentKeys {
			if !newKeys.Contains(apiKey) {
				keyEvents <- NewRemovedKeyEvent(key)
				delete(currentKeys, apiKey)
			}
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

func NewDrpcIntegrationClientWithConnector(connector drpc.DrpcHttpConnector, pollInterval time.Duration) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		connector:    connector,
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: pollInterval,
	}
}

func NewDrpcIntegrationClient(
	drpcIntegration *config.DrpcIntegrationConfig,
) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		connector:    drpc.NewSimpleDrpcHttpConnector(drpcIntegration),
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: 1 * time.Minute,
	}
}

var _ IntegrationClient = (*DrpcIntegrationClient)(nil)
