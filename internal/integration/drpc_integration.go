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

type DrpcOwnedKey struct {
	OwnerID  string
	ApiToken string
	ApiKey   string
}

type DrpcIntegrationClient struct {
	ctx              context.Context
	connector        drpc.DrpcHttpConnector
	ownerKeys        *utils.CMap[string, map[string]*drpc.DrpcKey]
	keyOwnersMapping *utils.CMap[string, DrpcOwnedKey]

	pollInterval time.Duration

	maxCap int
}

func NewDrpcIntegrationClientWithConnector(ctx context.Context, connector drpc.DrpcHttpConnector, pollInterval time.Duration) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:              ctx,
		connector:        connector,
		ownerKeys:        utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		keyOwnersMapping: utils.NewCMap[string, DrpcOwnedKey](),
		pollInterval:     pollInterval,
		maxCap:           8192,
	}
}

func NewDrpcIntegrationClient(
	drpcIntegration *config.DrpcIntegrationConfig,
) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:              context.Background(),
		connector:        drpc.NewSimpleDrpcHttpConnector(drpcIntegration),
		ownerKeys:        utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		keyOwnersMapping: utils.NewCMap[string, DrpcOwnedKey](),
		pollInterval:     1 * time.Minute,
		maxCap:           8192,
	}
}

var (
	_ IntegrationClient = (*DrpcIntegrationClient)(nil)

	ErrStatsDataCorrupted = errors.New("stats data corrupted")
)

type (
	statsData = *utils.CMap[statsdata.StatsKey, statsdata.StatsData]

	statsUploadBatch struct {
		OwnerID  string
		ApiToken string
		Entries  []*api.StatsEntry
	}
)

func (d *DrpcIntegrationClient) ProcessStatsData(statsMap statsData) error {
	itemsPerKey := make(map[string]*statsUploadBatch, 128)

	statsMap.Range(func(k statsdata.StatsKey, v statsdata.StatsData) bool {
		if k.ApiKey == "" {
			log.Warn().Msg("process stats data: api key is empty")
			return true
		}
		data, ok := v.(*statsdata.RequestStatsData)
		if !ok {
			log.Warn().Str("api_key", k.ApiKey).Msgf("process stats data: stats data has unexpected type %T", v)
			return true
		}
		ownerData, ok := d.keyOwnersMapping.Load(k.ApiKey)
		if !ok {
			log.Warn().Str("api_key", k.ApiKey).Msg("process stats data: api key owner is missing")
			return true
		}

		if itemsPerKey[k.ApiKey] == nil {
			itemsPerKey[k.ApiKey] = &statsUploadBatch{
				OwnerID:  ownerData.OwnerID,
				ApiToken: ownerData.ApiToken,
				Entries:  make([]*api.StatsEntry, 0, 128),
			}
		}

		itemsPerKey[k.ApiKey].Entries = append(itemsPerKey[k.ApiKey].Entries, &api.StatsEntry{
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
	if len(itemsPerKey) == 0 {
		return fmt.Errorf("process stats data: no keys found")
	}

	g := new(errgroup.Group)
	for _, uploadBatch := range itemsPerKey {
		uploadBatch := uploadBatch //nolint:modernize
		g.Go(func() error {
			payload := &api.StatsBatch{
				Entries: uploadBatch.Entries,
			}
			bt, err := proto.Marshal(payload)
			if err != nil {
				return fmt.Errorf("couldn't marshal batch %w: %v", ErrStatsDataCorrupted, err)
			}
			return d.connector.UploadStats(bt, uploadBatch.OwnerID, uploadBatch.ApiToken)
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
	log.Info().Msgf("polled %d drpc keys for owner %s", len(ownerKeys), ownerId)

	newKeys := mapset.NewThreadUnsafeSet[string]()
	for _, key := range ownerKeys {
		apiKey := key.GetKeyValue()
		newKeys.Add(key.GetKeyValue())
		currentKey, ok := currentKeys[key.GetKeyValue()]

		if !ok || !cmp.Equal(currentKey, key) {
			keyEvents <- keydata.NewUpdatedKeyEvent(key)
			currentKeys[key.GetKeyValue()] = key
		}

		d.keyOwnersMapping.Store(apiKey, DrpcOwnedKey{
			OwnerID:  ownerId,
			ApiToken: apiToken,
			ApiKey:   apiKey,
		})
	}

	for apiKey, key := range currentKeys {
		if !newKeys.Contains(apiKey) {
			keyEvents <- keydata.NewRemovedKeyEvent(key)
			delete(currentKeys, apiKey)
			d.keyOwnersMapping.Delete(apiKey)
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
