package integration

import (
	"context"
	"errors"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats/api"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
	"google.golang.org/protobuf/proto"
	"sync"
	"testing"
)

type uploadStatsCall struct {
	payload  []byte
	ownerID  string
	apiToken string
}

type drpcHttpConnectorMock struct {
	mu sync.Mutex

	uploadStatsCalls []uploadStatsCall
	uploadStatsError error
}

func (m *drpcHttpConnectorMock) OwnerExists(ownerID, apiToken string) error {
	return nil
}

func (m *drpcHttpConnectorMock) LoadOwnerKeys(ownerID, apiToken string) ([]*drpc.DrpcKey, error) {
	return nil, nil
}

func (m *drpcHttpConnectorMock) UploadStats(payload []byte, ownerID, apiToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.uploadStatsCalls = append(m.uploadStatsCalls, uploadStatsCall{
		payload:  payload,
		ownerID:  ownerID,
		apiToken: apiToken,
	})

	return m.uploadStatsError
}

func TestDrpcIntegrationClient_ProcessStatsData_UploadsGroupedEntriesPerAPIKey(t *testing.T) {
	connectorMock := &drpcHttpConnectorMock{}
	client := &DrpcIntegrationClient{
		ctx:              context.Background(),
		connector:        connectorMock,
		ownerKeys:        utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		keyOwnersMapping: utils.NewCMap[string, DrpcOwnedKey](),
		pollInterval:     0,
		maxCap:           8192,
	}

	client.keyOwnersMapping.Store("api-key-1", DrpcOwnedKey{
		OwnerID:  "owner-1",
		ApiToken: "token-1",
		ApiKey:   "api-key-1",
	})
	client.keyOwnersMapping.Store("api-key-2", DrpcOwnedKey{
		OwnerID:  "owner-2",
		ApiToken: "token-2",
		ApiKey:   "api-key-2",
	})

	reqData1 := &statsdata.RequestStatsData{}
	reqData1.AddRequest()
	reqData1.AddRequest()
	reqData1.AddRequest()

	statsMap := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  100,
			UpstreamId: "upstream-1",
			Method:     "eth_call",
			ApiKey:     "api-key-1",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      1,
		},
		reqData1,
	)
	reqData2 := &statsdata.RequestStatsData{}
	for range 5 {
		reqData2.AddRequest()
	}
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  101,
			UpstreamId: "upstream-2",
			Method:     "eth_getBalance",
			ApiKey:     "api-key-1",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      1,
		},
		reqData2,
	)
	reqData3 := &statsdata.RequestStatsData{}
	for range 7 {
		reqData3.AddRequest()
	}
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  102,
			UpstreamId: "upstream-3",
			Method:     "eth_blockNumber",
			ApiKey:     "api-key-2",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      10,
		},
		reqData3,
	)

	err := client.ProcessStatsData(statsMap)
	if err != nil {
		t.Fatalf("ProcessStatsData returned error: %v", err)
	}

	if len(connectorMock.uploadStatsCalls) != 2 {
		t.Fatalf("expected 2 UploadStats calls, got %d", len(connectorMock.uploadStatsCalls))
	}

	callsByOwner := make(map[string]uploadStatsCall, 2)
	for _, call := range connectorMock.uploadStatsCalls {
		callsByOwner[call.ownerID] = call
	}

	callOwner1, ok := callsByOwner["owner-1"]
	if !ok {
		t.Fatalf("expected upload call for owner-1")
	}
	if callOwner1.apiToken != "token-1" {
		t.Fatalf("expected token-1 for owner-1, got %q", callOwner1.apiToken)
	}

	var batchOwner1 api.StatsBatch
	if err := proto.Unmarshal(callOwner1.payload, &batchOwner1); err != nil {
		t.Fatalf("failed to unmarshal owner-1 payload: %v", err)
	}
	if len(batchOwner1.Entries) != 2 {
		t.Fatalf("expected 2 entries for owner-1, got %d", len(batchOwner1.Entries))
	}

	callOwner2, ok := callsByOwner["owner-2"]
	if !ok {
		t.Fatalf("expected upload call for owner-2")
	}
	if callOwner2.apiToken != "token-2" {
		t.Fatalf("expected token-2 for owner-2, got %q", callOwner2.apiToken)
	}

	var batchOwner2 api.StatsBatch
	if err := proto.Unmarshal(callOwner2.payload, &batchOwner2); err != nil {
		t.Fatalf("failed to unmarshal owner-2 payload: %v", err)
	}
	if len(batchOwner2.Entries) != 1 {
		t.Fatalf("expected 1 entry for owner-2, got %d", len(batchOwner2.Entries))
	}
	if batchOwner2.Entries[0].Key.ApiKey != "api-key-2" {
		t.Fatalf("expected api-key-2 in owner-2 batch, got %q", batchOwner2.Entries[0].Key.ApiKey)
	}
}

func TestDrpcIntegrationClient_ProcessStatsData_SkipsEntriesWithoutAPIKeyOrOwnerMapping(t *testing.T) {
	connectorMock := &drpcHttpConnectorMock{}
	client := &DrpcIntegrationClient{
		ctx:              context.Background(),
		connector:        connectorMock,
		ownerKeys:        utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		keyOwnersMapping: utils.NewCMap[string, DrpcOwnedKey](),
		pollInterval:     0,
		maxCap:           8192,
	}

	reqData1 := &statsdata.RequestStatsData{}
	reqData1.AddRequest()
	statsMap := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  200,
			UpstreamId: "upstream-empty",
			Method:     "eth_call",
			ApiKey:     "",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      1,
		},
		reqData1,
	)
	reqData2 := &statsdata.RequestStatsData{}
	reqData2.AddRequest()
	reqData2.AddRequest()
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  201,
			UpstreamId: "upstream-missing-owner",
			Method:     "eth_call",
			ApiKey:     "unknown-api-key",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      1,
		},
		reqData2,
	)

	err := client.ProcessStatsData(statsMap)
	if err != nil {
		t.Fatalf("ProcessStatsData returned error: %v", err)
	}

	if len(connectorMock.uploadStatsCalls) != 0 {
		t.Fatalf("expected 0 UploadStats calls, got %d", len(connectorMock.uploadStatsCalls))
	}
}

func TestDrpcIntegrationClient_ProcessStatsData_ReturnsUploadError(t *testing.T) {
	expectedError := errors.New("upload failed")

	connectorMock := &drpcHttpConnectorMock{
		uploadStatsError: expectedError,
	}
	client := &DrpcIntegrationClient{
		ctx:              context.Background(),
		connector:        connectorMock,
		ownerKeys:        utils.NewCMap[string, map[string]*drpc.DrpcKey](),
		keyOwnersMapping: utils.NewCMap[string, DrpcOwnedKey](),
		pollInterval:     0,
		maxCap:           8192,
	}

	client.keyOwnersMapping.Store("api-key-1", DrpcOwnedKey{
		OwnerID:  "owner-1",
		ApiToken: "token-1",
		ApiKey:   "api-key-1",
	})

	reqData1 := &statsdata.RequestStatsData{}
	for range 9 {
		reqData1.AddRequest()
	}

	statsMap := utils.NewCMap[statsdata.StatsKey, statsdata.StatsData]()
	statsMap.Store(
		statsdata.StatsKey{
			Timestamp:  300,
			UpstreamId: "upstream-1",
			Method:     "eth_call",
			ApiKey:     "api-key-1",
			ReqKind:    protocol.UnknownReqKind,
			RespKind:   protocol.UnknownRespKind,
			Chain:      1,
		},
		reqData1,
	)

	err := client.ProcessStatsData(statsMap)
	if !errors.Is(err, expectedError) {
		t.Fatalf("expected error %v, got %v", expectedError, err)
	}

	if len(connectorMock.uploadStatsCalls) != 1 {
		t.Fatalf("expected 1 UploadStats call, got %d", len(connectorMock.uploadStatsCalls))
	}
}
