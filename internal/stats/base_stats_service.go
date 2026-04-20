package stats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/outbox"
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

	outbox       outbox.Storer
	outboxCursor atomic.Int64

	once     sync.Once
	stopChan chan struct{}
	waitChan chan struct{}
}

type statsMap = *utils.CMap[statsdata.StatsKey, statsdata.StatsData]

type statsDataHolder struct {
	counter             atomic.Int64
	closed              atomic.Bool
	statsAggregatedData statsMap
}

func newStatsDataHolder() *statsDataHolder {
	return &statsDataHolder{
		statsAggregatedData: utils.NewCMap[statsdata.StatsKey, statsdata.StatsData](),
	}
}

func (b *BaseStatsService) Start(outbox outbox.Storer) {
	b.outbox = outbox

	go b.process()
}

func (b *BaseStatsService) Stop(ctx context.Context) error {
	b.enabled.Store(false)
	b.once.Do(func() {
		close(b.stopChan)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.waitChan:
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
	defer close(b.waitChan)

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

	aggregated := current.statsAggregatedData
	err := b.integrationClient.ProcessStatsData(aggregated)
	if errors.Is(err, integration.ErrStatsDataCorrupted) {
		log.Error().Err(err).Msg("stats data: corrupted")
		return err
	}
	if err == nil {
		return nil
	}
	log.Error().Err(err).Msg("stats data: processing failed")
	return b.storeUnprocessed(aggregated)
}

func (b *BaseStatsService) flushUnprocessed() error {
	stats, keys, err := b.listUnprocessed()
	if err != nil {
		return err
	}
	if stats == nil {
		b.outboxCursor.Store(0)
		return nil
	}

	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()

	err = b.integrationClient.ProcessStatsData(stats)
	if err != nil {
		log.Error().Err(err).Msg("stats: cannot store unprocessed data")
		return err
	}
	for _, key := range keys {
		if err := b.outbox.Delete(ctx, key); err != nil {
			log.Error().Err(err).Msg("stats: cannot remove outbox data")
		}
	}
	b.outboxCursor.Add(int64(len(keys)))
	return nil
}

func (b *BaseStatsService) storeUnprocessed(aggregated statsMap) error {
	data, err := marshalStatsMap(aggregated)
	if err != nil {
		return err
	}
	key := hashToString(data)
	compressed := compressStats(data)

	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()
	return b.outbox.Set(ctx, key, compressed, time.Hour*24)
}

const defaultLimit = 5

func (b *BaseStatsService) listUnprocessed() (statsMap, []string, error) {
	ctx, cancelF := context.WithTimeout(b.ctx, time.Second*5)
	defer cancelF()

	result, err := b.outbox.List(ctx, b.outboxCursor.Load(), defaultLimit)
	if err != nil {
		return nil, nil, err
	}
	if len(result) == 0 {
		return nil, nil, nil
	}

	mergedStats := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()
	keys := make([]string, 0, len(result))

	for _, item := range result {
		keys = append(keys, item.Key)

		decompressed := decompressStats(item.Value)

		currentStats, err := unmarshalStatsMap(decompressed)
		if err != nil {
			return nil, nil, err
		}
		currentStats.Range(func(key statsdata.StatsKey, value statsdata.StatsData) bool {
			mergedStats.Store(key, value)
			return true
		})
	}
	return mergedStats, keys, nil
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
		waitChan:           make(chan struct{}),
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
		waitChan:           make(chan struct{}),
	}

	return statsService
}

var _ StatsService = (*BaseStatsService)(nil)

type persistedStatsItem struct {
	Key           statsdata.StatsKey `json:"key"`
	RequestAmount int64              `json:"request_amount"`
}

// TODO that's bad. Get rid of a CMap
func marshalStatsMap(stats statsMap) ([]byte, error) {
	items := make([]persistedStatsItem, 0)

	stats.Range(func(key statsdata.StatsKey, value statsdata.StatsData) bool {
		requestStatsData, ok := value.(*statsdata.RequestStatsData)
		if !ok {
			return true
		}

		items = append(items, persistedStatsItem{
			Key:           key,
			RequestAmount: requestStatsData.GetRequestAmount(),
		})
		return true
	})

	return sonic.Marshal(items)
}

// TODO that's bad. Get rid of a CMap
func unmarshalStatsMap(data []byte) (statsMap, error) {
	var items []persistedStatsItem
	if err := sonic.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	result := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()

	for _, item := range items {
		requestStatsData := statsdata.NewRequestStatsData()
		for requestIndex := int64(0); requestIndex < item.RequestAmount; requestIndex++ {
			requestStatsData.AddRequest()
		}

		result.Store(item.Key, requestStatsData)
	}

	return result, nil
}
