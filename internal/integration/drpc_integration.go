package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/nodecore/internal/stats/api"
	"google.golang.org/protobuf/proto"
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

	ownerID, apiToken string
}

type statsData = *utils.CMap[statsdata.StatsKey, statsdata.StatsData]

var ErrStatsDataCorrupted = errors.New("stats data corrupted")

func (d *DrpcIntegrationClient) ProcessStatsData(statsMap statsData) (unprocessed []byte, err error) {
	log.Debug().Msgf("processing stats data with size: %d", statsMap.Size())

	if d.ownerID == "" || d.apiToken == "" {
		return nil, fmt.Errorf("stats: integration client has no credentials")
	}

	batch := new(api.StatsBatch)
	entries := make([]*api.StatsEntry, 0, statsMap.Size())

	statsMap.Range(func(k statsdata.StatsKey, v statsdata.StatsData) bool {
		data, ok := v.(*statsdata.RequestStatsData)
		if !ok {
			return true
		}
		entries = append(entries, &api.StatsEntry{
			Key: &api.StatsKey{
				Timestamp:  k.Timestamp,
				UpstreamId: k.UpstreamId,
				Method:     k.Method,
				ApiKey:     k.ApiKey,
				ReqKind:    api.RequestKind(k.ReqKind),
				RespKind:   api.ResponseKind(k.RespKind),
				Chain:      int64(k.Chain),
			},
			Data: &api.RequestStatsData{RequestAmount: data.GetRequestAmount()},
		})
		return true
	})

	batch.Entries = entries

	bt, err := proto.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal batch %w: %w", ErrStatsDataCorrupted, err)
	}

	return bt, d.connector.UploadStats(bt, d.ownerID, d.apiToken)
}

func (d *DrpcIntegrationClient) ProcessStatsDataRaw(data []byte) error {
	return d.connector.UploadStats(data, d.ownerID, d.apiToken)
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

	d.ownerID, d.apiToken = drpcKeyCfg.Owner.Id, drpcKeyCfg.Owner.ApiToken
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
		log.Error().Err(err).Msgf("error polling drpc keys for owner '%s'", ownerId)
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
