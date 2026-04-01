package stats

import (
	"bytes"
	"context"
	"errors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/klauspost/compress/gzip"
	"io"
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
	enabled            *atomic.Bool
	integrationClient  integration.IntegrationClient
	statsDataHolder    *atomic.Pointer[statsDataHolder]
	statsFlushInterval time.Duration

	outbox StatsOutboxStorer

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

func (b *BaseStatsService) Start(outbox StatsOutboxStorer) {
	b.outbox = outbox

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
	if !b.enabled.Load() {
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

	if !b.enabled.Load() {
		return
	}

	ticker := time.NewTicker(b.statsFlushInterval)
	defer ticker.Stop()

	outboxFlushTicker := time.NewTicker(b.statsFlushInterval * 2)
	defer outboxFlushTicker.Stop()

	for {
		select {
		case <-outboxFlushTicker.C:
		case <-ticker.C:
			go b.flush()
		case <-b.ctx.Done():
			b.flush()
			return
		}
	}
}

func (b *BaseStatsService) flush() {
	current := b.statsDataHolder.Swap(newStatsDataHolder())

	for current.counter.Load() != 0 {
		time.Sleep(50 * time.Millisecond)
	}

	unprocessed, err := b.integrationClient.ProcessStatsData(current.statsAggregatedData)
	if errors.Is(err, integration.ErrStatsDataCorrupted) {
		log.Error().Err(err).Msg("stats: cannot marshal data")
		return
	}
	if err == nil {
		return
	}

	data, err := compressStats(unprocessed)
	if err != nil {
		log.Error().Err(err).Msg("stats: cannot compress data")
		data = unprocessed
	}

	b.storeUnprocessed(data)
}

//func (b *BaseStatsService) flushUnprocessed() {
//
//
//	unprocessed, err := b.integrationClient.ProcessStatsData(current.statsAggregatedData)
//	if errors.Is(err, integration.ErrStatsDataCorrupted) {
//		log.Error().Err(err).Msg("stats: cannot marshal data")
//		return
//	}
//	if err == nil {
//		return
//	}
//
//	data, err := compressStats(unprocessed)
//	if err != nil {
//		log.Error().Err(err).Msg("stats: cannot compress data")
//		data = unprocessed
//	}
//
//	b.storeUnprocessed(data)
//}

func (b *BaseStatsService) storeUnprocessed(data []byte) {
	ctx, cancelF := context.WithTimeout(context.Background(), time.Second)
	defer cancelF()

	b.outbox.Store(ctx, chains.Unknown, req, data)
}

func (b *BaseStatsService) retrieveUnprocessed(req protocol.RequestHolder) ([]byte, bool) {
	ctx, cancelF := context.WithTimeout(context.Background(), time.Second)
	defer cancelF()

	return b.outbox.Receive(ctx, chains.Unknown, req)
}

func compressStats(data []byte) ([]byte, error) {
	var compressed bytes.Buffer

	gzipWriter := gzip.NewWriter(&compressed)

	if _, err := gzipWriter.Write(data); err != nil {
		return nil, err
	}

	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}

	return compressed.Bytes(), nil
}

func decompressStats(compressed []byte) ([]byte, error) {
	if len(compressed) == 0 {
		return nil, nil
	}
	if len(compressed) < 2 || compressed[0] != 0x1f || compressed[1] != 0x8b {
		notCompressed := compressed
		return notCompressed, nil
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer gzReader.Close()

	uncompressedData, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, err
	}
	return uncompressedData, err
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

	isEnabled := new(atomic.Bool)
	isEnabled.Store(statsConfig.Enabled)
	statsService := &BaseStatsService{
		ctx:                ctx,
		statsFlushInterval: statsConfig.FlushInterval,
		enabled:            isEnabled,
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

	isEnabled := new(atomic.Bool)
	isEnabled.Store(statsConfig.Enabled)
	statsService := &BaseStatsService{
		ctx:                ctx,
		statsFlushInterval: statsConfig.FlushInterval,
		enabled:            isEnabled,
		integrationClient:  integrationClient,
		statsDataHolder:    &statsDataHolderPointer,
	}

	return statsService
}

var _ StatsService = (*BaseStatsService)(nil)
