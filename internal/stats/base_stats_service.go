package stats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/klauspost/compress/gzip"
	"hash"
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

	outbox       StatsOutboxStorer
	outboxCursor atomic.Int64

	stopChan chan struct{}
	doneChan chan struct{}
}

type statsDataHolder struct {
	counter             atomic.Int64
	closed              atomic.Bool
	statsAggregatedData *utils.CMap[statsdata.StatsKey, statsdata.StatsData]
}

func newStatsDataHolder() *statsDataHolder {
	return &statsDataHolder{
		statsAggregatedData: utils.NewCMap[statsdata.StatsKey, statsdata.StatsData](),
	}
}

func (b *BaseStatsService) Start(outbox StatsOutboxStorer) {
	b.outbox = outbox

	go b.process()
}

func (b *BaseStatsService) Stop(ctx context.Context) error {
	b.enabled.Store(false)
	sync.OnceFunc(func() {
		close(b.stopChan)
	})()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.doneChan:
		return nil
	}
}

func (b *BaseStatsService) AddRequestResults(requestResults []protocol.RequestResult) {
	if !b.enabled.Load() {
		return
	}
	for _, result := range requestResults {
		for {
			if b.ctx.Err() != nil {
				return
			}
			// use a lock-free approach to write in the map and get a snapshot of the map in parallel
			holder := b.statsDataHolder.Load()

			if holder.closed.Load() {
				continue
			}

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
	defer sync.OnceFunc(func() {
		close(b.doneChan)
	})()

	if !b.enabled.Load() {
		return
	}

	log.Info().Msg("stats service started")

	ticker := time.NewTicker(b.statsFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopChan:
			_ = b.flush()
			_ = b.flushUnprocessed()
			return
		case <-ticker.C:
			if err := b.flush(); err != nil {
				log.Error().Err(err).Msg("stats: cannot flush data")
			}
			if err := b.flushUnprocessed(); err != nil {
				log.Error().Err(err).Msg("failed to flush unprocessed stats")
			}
			log.Debug().Msg("stats flush finished")
		case <-b.ctx.Done():
			_ = b.flush()
			_ = b.flushUnprocessed()
			return
		}
	}
}

func (b *BaseStatsService) flush() error {
	current := b.statsDataHolder.Swap(newStatsDataHolder())
	current.closed.Store(true)

	for current.counter.Load() != 0 {
		time.Sleep(50 * time.Millisecond)
	}

	unprocessed, err := b.integrationClient.ProcessStatsData(current.statsAggregatedData)
	if errors.Is(err, integration.ErrStatsDataCorrupted) {
		log.Error().Err(err).Msg("stats: cannot marshal data")
		return err
	}
	if err != nil {
		return err
	}

	return b.storeUnprocessed(unprocessed)
}

func (b *BaseStatsService) flushUnprocessed() error {
	stats, err := b.listUnprocessed()
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		b.outboxCursor.Store(0)
		return nil
	}

	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()

	for _, stat := range stats {
		err := b.integrationClient.ProcessStatsDataRaw(stat.value)
		if err != nil {
			log.Error().Err(err).Msg("stats: cannot store unprocessed data")
			continue
		}
		if err := b.outbox.Delete(ctx, stat.key); err != nil {
			log.Error().Err(err).Msg("stats: cannot remove outbox data")
		}
	}
	b.outboxCursor.Add(int64(len(stats)))
	return nil
}

func (b *BaseStatsService) storeUnprocessed(data []byte) error {
	key := hashToString(data)
	compressed := compressStats(data)

	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()
	return b.outbox.Set(ctx, key, compressed, time.Hour*24)
}

type statsItem struct {
	key   string
	value []byte
}

func (b *BaseStatsService) listUnprocessed() ([]statsItem, error) {
	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()

	result, err := b.outbox.List(ctx, b.outboxCursor.Load(), 5)
	if err != nil {
		return nil, err
	}

	items := make([]statsItem, 0, len(result))
	for _, mp := range result {
		for key, v := range mp {
			items = append(items, statsItem{
				key:   key,
				value: decompressStats(v),
			})
		}
	}
	return items, nil
}

var gzipWriterPool = sync.Pool{
	New: func() any {
		writer, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return writer
	},
}

func compressStats(data []byte) []byte {
	var buffer bytes.Buffer

	writer := gzipWriterPool.Get().(*gzip.Writer)
	writer.Reset(&buffer)

	_, err := writer.Write(data)
	if err != nil {
		log.Error().Err(err).Msg("stats: cannot compress data")
		gzipWriterPool.Put(writer)
		return data
	}

	if err := writer.Close(); err != nil {
		log.Error().Err(err).Msg("stats: cannot close compress")
		gzipWriterPool.Put(writer)
		return data
	}

	gzipWriterPool.Put(writer)
	return buffer.Bytes()
}

var gzipReaderPool = sync.Pool{
	New: func() any {
		return new(gzip.Reader)
	},
}

func decompressStats(compressed []byte) []byte {
	if len(compressed) < 2 || compressed[0] != 0x1f || compressed[1] != 0x8b {
		return compressed
	}

	reader := gzipReaderPool.Get().(*gzip.Reader)

	if err := reader.Reset(bytes.NewReader(compressed)); err != nil {
		log.Error().Err(err).Msg("stats: cannot decompress data")
		gzipReaderPool.Put(reader)
		return compressed
	}

	defer func() {
		_ = reader.Close()
		gzipReaderPool.Put(reader)
	}()

	data, err := io.ReadAll(reader)
	if err != nil {
		log.Error().Err(err).Msg("stats: cannot read decompressed data")
		return compressed
	}
	return data
}

var hashPool = sync.Pool{
	New: func() any {
		return sha256.New()
	},
}

func hashToString(data []byte) string {
	hasher := hashPool.Get().(hash.Hash)
	defer hashPool.Put(hasher)

	hasher.Reset()
	_, _ = hasher.Write(data)

	return hex.EncodeToString(hasher.Sum(nil))
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
	if integrationClient == nil {
		panic(errors.New("stats: integration client is nil"))
	}
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
		stopChan:           make(chan struct{}),
		doneChan:           make(chan struct{}),
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
		stopChan:           make(chan struct{}),
		doneChan:           make(chan struct{}),
	}

	return statsService
}

var _ StatsService = (*BaseStatsService)(nil)
