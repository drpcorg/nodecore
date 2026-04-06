package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/drpcorg/nodecore/internal/stats/api"
	"google.golang.org/protobuf/proto"
	"sync"
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

	entriesPool sync.Pool
	maxCap      int

	ownerID, apiToken string
}

type statsEntryBuffer struct {
	items []*api.StatsEntry
}

func NewDrpcIntegrationClientWithConnector(ctx context.Context, connector drpc.DrpcHttpConnector, pollInterval time.Duration) *DrpcIntegrationClient {
	return &DrpcIntegrationClient{
		ctx:          ctx,
		connector:    connector,
		ownerKeys:    utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		pollInterval: pollInterval,
		maxCap:       8192,
		entriesPool: sync.Pool{
			New: func() any {
				return &statsEntryBuffer{
					items: make([]*api.StatsEntry, 0, 128),
				}
			},
		},
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
		entriesPool: sync.Pool{
			New: func() any {
				return &statsEntryBuffer{
					items: make([]*api.StatsEntry, 0, 128),
				}
			},
		},
	}
}

var (
	_ IntegrationClient = (*DrpcIntegrationClient)(nil)

	ErrStatsDataCorrupted = errors.New("stats data corrupted")
)

type statsData = *utils.CMap[statsdata.StatsKey, statsdata.StatsData]

func (d *DrpcIntegrationClient) ProcessStatsData(statsMap statsData) (unprocessed []byte, err error) {
	if d.ownerID == "" || d.apiToken == "" {
		return nil, fmt.Errorf("stats: integration client has no credentials")
	}

	buf := d.getEntries()
	defer d.putEntries(buf)

	statsMap.Range(func(k statsdata.StatsKey, v statsdata.StatsData) bool {
		data, ok := v.(*statsdata.RequestStatsData)
		if !ok {
			return true
		}
		buf.items = append(buf.items, &api.StatsEntry{
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

	batch := &api.StatsBatch{
		Entries: buf.items,
	}

	bt, err := proto.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal batch %w: %v", ErrStatsDataCorrupted, err)
	}
	if err := d.connector.UploadStats(bt, d.ownerID, d.apiToken); err != nil {
		return nil, fmt.Errorf("couldn't upload stats: %w", err)
	}
	return bt, nil
}

func (d *DrpcIntegrationClient) ProcessStatsDataRaw(data []byte) error {
	return d.connector.UploadStats(data, d.ownerID, d.apiToken)
}

func (d *DrpcIntegrationClient) getEntries() *statsEntryBuffer {
	buf := d.entriesPool.Get().(*statsEntryBuffer)
	buf.items = buf.items[:0]
	return buf
}

func (d *DrpcIntegrationClient) putEntries(buf *statsEntryBuffer) {
	if cap(buf.items) > d.maxCap {
		buf.items = make([]*api.StatsEntry, 0, d.maxCap/2)
	}
	d.entriesPool.Put(buf)
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
