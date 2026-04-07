package stats

import (
	"context"
	"errors"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/outbox"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockOutbox struct{}

func (m mockOutbox) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return nil
}

func (m mockOutbox) Delete(ctx context.Context, key string) error {
	return nil
}

func (m mockOutbox) List(ctx context.Context, cursor int64, limit int64) ([]outbox.Item, error) {
	return []outbox.Item{}, nil
}

func TestStatsServiceDisabledThenNoInterationToIntegrationClient(t *testing.T) {
	client := mocks.NewMockIntegrationClient("type")
	statsCfg := &config.StatsConfig{
		Enabled: false,
	}
	statsService := NewBaseStatsServiceWithIntegrationClient(context.Background(), statsCfg, client)
	statsService.Start(&mockOutbox{})

	statsService.AddRequestResults([]protocol.RequestResult{protocol.NewUnaryRequestResult()})

	client.AssertNotCalled(t, "ProcessStatsData", mock.Anything)
	client.AssertNotCalled(t, "GetStatsSchema")

	err := statsService.Stop(context.Background())
	assert.NoError(t, err)
}

func TestStatsServiceProcessStatsDataAndStop(t *testing.T) {
	client := mocks.NewMockIntegrationClient("type")
	statsCfg := &config.StatsConfig{
		Enabled:       true,
		FlushInterval: 50 * time.Millisecond,
	}
	result := protocol.NewUnaryRequestResult().WithUpstreamId("upId")
	result1 := protocol.NewUnaryRequestResult().WithUpstreamId("upId")

	client.On("GetStatsSchema").Return([]statsdata.StatsDims{statsdata.UpstreamId})
	client.On("ProcessStatsData", mock.Anything).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	statsService := NewBaseStatsServiceWithIntegrationClient(ctx, statsCfg, client)
	statsService.Start(&mockOutbox{})

	statsService.AddRequestResults([]protocol.RequestResult{result, result1})
	time.Sleep(80 * time.Millisecond)

	client.AssertExpectations(t)

	cancel()
	err := statsService.Stop(context.Background())
	assert.NoError(t, err)

	time.Sleep(30 * time.Millisecond)
	client.AssertNumberOfCalls(t, "ProcessStatsData", 2)
}

// ======================================= OUTBOX ===============================================

type fakeOutbox struct {
	items       []outbox.Item
	deleteError map[string]error
	setError    error
	listError   error

	setCalls    int
	listCalls   int
	deleteCalls int
}

func (f *fakeOutbox) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	f.setCalls++
	if f.setError != nil {
		return f.setError
	}
	f.items = append(f.items, outbox.Item{
		Key:   key,
		Value: value,
	})
	return nil
}

func (f *fakeOutbox) List(_ context.Context, cursor, limit int64) ([]outbox.Item, error) {
	f.listCalls++
	if f.listError != nil {
		return nil, f.listError
	}
	if cursor >= int64(len(f.items)) {
		return nil, nil
	}

	endIndex := cursor + limit
	if endIndex > int64(len(f.items)) {
		endIndex = int64(len(f.items))
	}

	result := make([]outbox.Item, 0, endIndex-cursor)
	result = append(result, f.items[cursor:endIndex]...)
	return result, nil
}

func (f *fakeOutbox) Delete(_ context.Context, key string) error {
	f.deleteCalls++
	if err := f.deleteError[key]; err != nil {
		return err
	}

	filteredItems := make([]outbox.Item, 0, len(f.items))
	for _, item := range f.items {
		if item.Key != key {
			filteredItems = append(filteredItems, item)
		}
	}
	f.items = filteredItems
	return nil
}

func newTestService(testContext context.Context, client integration.IntegrationClient) *BaseStatsService {
	statsConfig := &config.StatsConfig{
		Enabled:       true,
		FlushInterval: time.Hour,
	}

	return NewBaseStatsServiceWithIntegrationClient(
		testContext,
		statsConfig,
		client,
	)
}

func newRequestStatsMap(requestCount int, method string, timestamp int64) statsMap {
	result := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()

	statsKey := statsdata.StatsKey{
		Timestamp: timestamp,
		Method:    method,
		ReqKind:   protocol.UnknownReqKind,
		RespKind:  protocol.UnknownRespKind,
		Chain:     chains.Unknown,
	}

	requestStats := statsdata.NewRequestStatsData()
	for index := 0; index < requestCount; index++ {
		requestStats.AddRequest()
	}

	result.Store(statsKey, requestStats)
	return result
}

func TestCompressStats_RoundTrip(t *testing.T) {
	originalData := []byte(`{"hello":"world"}`)

	compressedData := compressStats(originalData)
	decompressedData := decompressStats(compressedData)

	if string(decompressedData) != string(originalData) {
		t.Fatalf("unexpected decompressed payload: got=%q want=%q", string(decompressedData), string(originalData))
	}
}

func TestStoreUnprocessed_StoresCompressedPayload(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")
	service := newTestService(testContext, client)
	service.outbox = outbox

	aggregatedStats := newRequestStatsMap(3, "eth_call", 100)

	err := service.storeUnprocessed(aggregatedStats)
	if err != nil {
		t.Fatalf("storeUnprocessed returned error: %v", err)
	}

	if outbox.setCalls != 1 {
		t.Fatalf("unexpected set calls: got=%d want=1", outbox.setCalls)
	}

	if len(outbox.items) != 1 {
		t.Fatalf("unexpected outbox size: got=%d want=1", len(outbox.items))
	}

	if len(outbox.items[0].Value) == 0 {
		t.Fatal("stored payload is empty")
	}

	decompressedPayload := decompressStats(outbox.items[0].Value)
	if len(decompressedPayload) == 0 {
		t.Fatal("decompressed payload is empty")
	}
}

func TestListUnprocessed_ReturnsMergedStatsAndKeys(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")
	service := newTestService(testContext, client)
	service.outbox = outbox

	firstStats := newRequestStatsMap(2, "eth_call", 100)
	secondStats := newRequestStatsMap(1, "eth_getBlockByNumber", 200)

	if err := service.storeUnprocessed(firstStats); err != nil {
		t.Fatalf("storeUnprocessed(firstStats) returned error: %v", err)
	}
	if err := service.storeUnprocessed(secondStats); err != nil {
		t.Fatalf("storeUnprocessed(secondStats) returned error: %v", err)
	}

	stats, keys, err := service.listUnprocessed()
	if err != nil {
		t.Fatalf("listUnprocessed returned error: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("unexpected key count: got=%d want=2", len(keys))
	}

	firstKey := statsdata.StatsKey{
		Timestamp: 100,
		Method:    "eth_call",
		RespKind:  protocol.UnknownRespKind,
		Chain:     chains.Unknown,
		ReqKind:   protocol.UnknownReqKind,
	}
	secondKey := statsdata.StatsKey{
		Timestamp: 200,
		Method:    "eth_getBlockByNumber",
		RespKind:  protocol.UnknownRespKind,
		Chain:     chains.Unknown,
		ReqKind:   protocol.UnknownReqKind,
	}

	if _, ok := stats.Load(firstKey); !ok {
		t.Fatalf("first key not found: %+v", firstKey)
	}
	if _, ok := stats.Load(secondKey); !ok {
		t.Fatalf("second key not found: %+v", secondKey)
	}
}

func TestFlushUnprocessed_SendsAndDeletesItems(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")
	service := newTestService(testContext, client)
	service.outbox = outbox

	firstStats := newRequestStatsMap(2, "eth_call", 100)
	secondStats := newRequestStatsMap(1, "eth_getBlockByNumber", 200)

	if err := service.storeUnprocessed(firstStats); err != nil {
		t.Fatalf("storeUnprocessed(firstStats) returned error: %v", err)
	}
	if err := service.storeUnprocessed(secondStats); err != nil {
		t.Fatalf("storeUnprocessed(secondStats) returned error: %v", err)
	}

	client.On("ProcessStatsData", mock.Anything).Return(nil).Once()

	err := service.flushUnprocessed()
	if err != nil {
		t.Fatalf("flushUnprocessed returned error: %v", err)
	}

	client.AssertNumberOfCalls(t, "ProcessStatsData", 1)

	if len(outbox.items) != 0 {
		t.Fatalf("outbox should be empty after successful flush, got=%d", len(outbox.items))
	}

	if service.outboxCursor.Load() != 2 {
		t.Fatalf("unexpected cursor after flush: got=%d want=2", service.outboxCursor.Load())
	}
}

func TestFlushUnprocessed_ResetsCursorWhenOutboxIsEmpty(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")
	service := newTestService(testContext, client)
	service.outbox = outbox
	service.outboxCursor.Store(123)

	err := service.flushUnprocessed()
	if err != nil {
		t.Fatalf("flushUnprocessed returned error: %v", err)
	}

	if service.outboxCursor.Load() != 0 {
		t.Fatalf("cursor was not reset: got=%d want=0", service.outboxCursor.Load())
	}

	client.AssertNotCalled(t, "ProcessStatsData", mock.Anything)
}

func TestFlushUnprocessed_DoesNotAdvanceCursorOnProcessError(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")

	service := newTestService(testContext, client)
	service.outbox = outbox

	aggregatedStats := newRequestStatsMap(2, "eth_call", 100)
	if err := service.storeUnprocessed(aggregatedStats); err != nil {
		t.Fatalf("storeUnprocessed returned error: %v", err)
	}

	client.On("ProcessStatsData", mock.Anything).
		Return(errors.New("integration failed")).
		Once()

	err := service.flushUnprocessed()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if service.outboxCursor.Load() != 0 {
		t.Fatalf("cursor must not advance on process error: got=%d want=0", service.outboxCursor.Load())
	}

	if len(outbox.items) != 1 {
		t.Fatalf("outbox item must remain after process error: got=%d want=1", len(outbox.items))
	}

	client.AssertNumberOfCalls(t, "ProcessStatsData", 1)
}

func TestListUnprocessed_RespectsCursorAndLimit(t *testing.T) {
	testContext := context.Background()
	outbox := &fakeOutbox{}
	client := mocks.NewMockIntegrationClient("type")
	service := newTestService(testContext, client)
	service.outbox = outbox

	for index := 0; index < defaultLimit+2; index++ {
		stats := newRequestStatsMap(1, "method", int64(index))
		if err := service.storeUnprocessed(stats); err != nil {
			t.Fatalf("storeUnprocessed(%d) returned error: %v", index, err)
		}
	}

	stats, keys, err := service.listUnprocessed()
	if err != nil {
		t.Fatalf("listUnprocessed returned error: %v", err)
	}

	if len(keys) != defaultLimit {
		t.Fatalf("unexpected keys count: got=%d want=%d", len(keys), defaultLimit)
	}

	for index := 0; index < defaultLimit; index++ {
		statsKey := statsdata.StatsKey{
			Timestamp: int64(index),
			Method:    "method",
			RespKind:  protocol.UnknownRespKind,
			Chain:     chains.Unknown,
			ReqKind:   protocol.UnknownReqKind,
		}
		if _, ok := stats.Load(statsKey); !ok {
			t.Fatalf("missing key at first page: %+v", statsKey)
		}
	}

	service.outboxCursor.Store(defaultLimit)

	secondPageStats, secondPageKeys, err := service.listUnprocessed()
	if err != nil {
		t.Fatalf("listUnprocessed second page returned error: %v", err)
	}

	if len(secondPageKeys) != 2 {
		t.Fatalf("unexpected second page key count: got=%d want=2", len(secondPageKeys))
	}

	for index := defaultLimit; index < defaultLimit+2; index++ {
		statsKey := statsdata.StatsKey{
			Timestamp: int64(index),
			Method:    "method",
			RespKind:  protocol.UnknownRespKind,
			Chain:     chains.Unknown,
			ReqKind:   protocol.UnknownReqKind,
		}
		if _, ok := secondPageStats.Load(statsKey); !ok {
			t.Fatalf("missing key at second page: %+v", statsKey)
		}
	}
}
