package stats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
)

type BaseStatsService struct {
	ctx                context.Context
	enabled            bool
	integrationClient  integration.IntegrationClient
	statsDataHolder    *atomic.Pointer[statsDataHolder]
	statsFlushInterval time.Duration

	wg sync.WaitGroup
}

type statsDataHolder struct {
	counter             atomic.Int64
	statsAggregatedData *utils.CMap[statsdata.StatsKey, statsdata.StatsData]
}

func newStatsDataHolder() *statsDataHolder {
	return &statsDataHolder{
		statsAggregatedData: utils.NewCMap[statsdata.StatsKey, statsdata.StatsData](),
	}
}

func (b *BaseStatsService) Start() {
	b.wg.Add(1)
	go b.process()
}

func (b *BaseStatsService) Stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Info().Msg("stats service stopped gracefully")
		return nil
	case <-ctx.Done():
		log.Warn().Msg("stats service stopped with timeout")
		return errors.New("timeout waiting for stats service to stop")
	}
}

func (b *BaseStatsService) AddRequestResults(requestResults []protocol.RequestResult) {
	if !b.enabled {
		return
	}
	for _, result := range requestResults {
		for {
			// use a lock-free approach to write in the map and get a snapshot of the map in parallel
			holder := b.statsDataHolder.Load()
			holder.counter.Add(1)

			// if holder changed while updating then retry
			if b.statsDataHolder.Load() != holder {
				holder.counter.Add(-1)
				continue
			}

			switch r := result.(type) {
			case *protocol.UnaryRequestResult:
				key := b.extractUnaryRequestKey(r)
				statsData, _ := holder.statsAggregatedData.LoadOrStore(key, statsdata.NewRequestStatsData())
				if requestStatsData, ok := statsData.(*statsdata.RequestStatsData); ok {
					requestStatsData.AddRequest()
				}
			}

			holder.counter.Add(-1)
			break
		}
	}
}

func (b *BaseStatsService) process() {
	defer b.wg.Done()
	if !b.enabled {
		return
	}
	stop := false

	for !stop {
		select {
		case <-time.After(b.statsFlushInterval):
			log.Debug().Msg("flushing stats...")
		case <-b.ctx.Done():
			log.Debug().Msg("stopping stats aggregation, flushing remaining data...")
			stop = true
		}
		currentAggregatedData := b.statsDataHolder.Swap(newStatsDataHolder())
		// wait until all updates to the old holder are done
		for currentAggregatedData.counter.Load() != 0 {
			log.Debug().Msg("waiting for stats aggregation to finish...")
			time.Sleep(200 * time.Millisecond)
		}
		currentAggregatedData.statsAggregatedData.Range(func(key statsdata.StatsKey, val statsdata.StatsData) bool {
			if v, ok := val.(*statsdata.RequestStatsData); ok {
				fmt.Println(key, v.GetRequestAmount())
			}
			return true
		})
		go b.integrationClient.ProcessStatsData(currentAggregatedData.statsAggregatedData)
	}
}

func (b *BaseStatsService) extractUnaryRequestKey(requestResult *protocol.UnaryRequestResult) statsdata.StatsKey {
	key := statsdata.StatsKey{
		// round to the nearest 5 minutes to avoid too many stats entries,
		// but it also could be configurable through the local config or an integration client
		Timestamp: requestResult.GetTimestamp().Truncate(5 * time.Minute).Unix(),
	}
	for _, dim := range b.integrationClient.GetStatsSchema() {
		switch dim {
		case statsdata.Method:
			key.Method = requestResult.GetMethod()
		case statsdata.UpstreamId:
			key.UpstreamId = requestResult.GetUpstreamId()
		case statsdata.ReqKind:
			key.ReqKind = requestResult.GetReqKind()
		case statsdata.RespKind:
			key.RespKind = requestResult.GetRespKind()
		case statsdata.ApiKey:
			key.ApiKey = requestResult.GetApiKey()
		case statsdata.Chain:
			key.Chain = requestResult.GetChain()
		}
	}

	return key
}

func NewBaseStatsService(
	ctx context.Context,
	statsConfig *config.StatsConfig,
	integrationResolver *integration.IntegrationResolver,
) *BaseStatsService {
	integrationClient := integrationResolver.GetIntegration(integration.GetIntegrationType(statsConfig.Type))
	var statsDataHolderPointer atomic.Pointer[statsDataHolder]
	statsDataHolderPointer.Store(newStatsDataHolder())

	statsService := &BaseStatsService{
		ctx:                ctx,
		statsFlushInterval: statsConfig.FlushInterval,
		enabled:            statsConfig.Enabled,
		integrationClient:  integrationClient,
		statsDataHolder:    &statsDataHolderPointer,
	}

	return statsService
}

func NewBaseStatsServiceWithIntegrationClient(
	ctx context.Context,
	statsConfig *config.StatsConfig,
	integrationClient integration.IntegrationClient,
) *BaseStatsService {
	var statsDataHolderPointer atomic.Pointer[statsDataHolder]
	statsDataHolderPointer.Store(newStatsDataHolder())

	statsService := &BaseStatsService{
		ctx:                ctx,
		statsFlushInterval: statsConfig.FlushInterval,
		enabled:            statsConfig.Enabled,
		integrationClient:  integrationClient,
		statsDataHolder:    &statsDataHolderPointer,
	}

	return statsService
}

var _ StatsService = (*BaseStatsService)(nil)
