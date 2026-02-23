package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestStatsServiceDisabledThenNoInterationToIntegrationClient(t *testing.T) {
	client := mocks.NewMockIntegrationClient("type")
	statsCfg := &config.StatsConfig{
		Enabled: false,
	}
	statsService := stats.NewBaseStatsServiceWithIntegrationClient(context.Background(), statsCfg, client)
	statsService.Start()

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
	client.On("ProcessStatsData", mock.Anything).Return()

	ctx, cancel := context.WithCancel(context.Background())
	statsService := stats.NewBaseStatsServiceWithIntegrationClient(ctx, statsCfg, client)
	statsService.Start()

	statsService.AddRequestResults([]protocol.RequestResult{result, result1})
	time.Sleep(80 * time.Millisecond)

	client.AssertExpectations(t)

	cancel()
	err := statsService.Stop(context.Background())
	assert.NoError(t, err)

	time.Sleep(30 * time.Millisecond)
	client.AssertNumberOfCalls(t, "ProcessStatsData", 2)
}
