package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/nodecore/internal/stats/api"
	"golang.org/x/sync/errgroup"
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

	maxCap int
}

func NewDrpcIntegrationClientWithConnector(ctx context.Context, connector drpc.DrpcHttpConnector, pollInterval time.Duration) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:          ctx,
		connector:    connector,
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: pollInterval,
		maxCap:       8192,
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
		maxCap:       8192,
	}
}

var (
	_ IntegrationClient = (*DrpcIntegrationClient)(nil)

	ErrStatsDataCorrupted = errors.New("stats data corrupted")
)

type statsData = *utils.CMap[statsdata.StatsKey, statsdata.StatsData]

func (d *DrpcIntegrationClient) ProcessStatsData(statsMap statsData) error {
	itemsPerKey := make(map[string][]*api.StatsEntry, 128)
	defer func() {
		for key := range itemsPerKey {
			delete(itemsPerKey, key)
		}
	}()

	statsMap.Range(func(k statsdata.StatsKey, v statsdata.StatsData) bool {
		data, ok := v.(*statsdata.RequestStatsData)
		if !ok {
			return true
		}
		if itemsPerKey[k.ApiKey] == nil {
			itemsPerKey[k.ApiKey] = make([]*api.StatsEntry, 0, 128)
		}
		itemsPerKey[k.ApiKey] = append(itemsPerKey[k.ApiKey], &api.StatsEntry{
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

	g := new(errgroup.Group)
	for apiKey, items := range itemsPerKey {
		g.Go(func() error {
			batch := &api.StatsBatch{
				Entries: items,
			}
			bt, err := proto.Marshal(batch)
			if err != nil {
				return fmt.Errorf("couldn't marshal batch %w: %v", ErrStatsDataCorrupted, err)
			}
			return d.connector.UploadStats(bt, apiKey)
		})
	}
	return g.Wait()
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
