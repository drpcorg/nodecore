package mocks

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/stats/statsdata"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type MockIntegrationClient struct {
	mock.Mock

	integrationType integration.IntegrationType
}

func NewMockIntegrationClient(integrationType integration.IntegrationType) *MockIntegrationClient {
	return &MockIntegrationClient{
		integrationType: integrationType,
	}
}

func (m *MockIntegrationClient) InitKeys(id string, cfg config.IntegrationKeyConfig) (chan keydata.KeyEvent, error) {
	args := m.Called(id, cfg)
	var data chan keydata.KeyEvent
	if args.Get(0) == nil {
		data = nil
	} else {
		data = args.Get(0).(chan keydata.KeyEvent)
	}
	return data, args.Error(1)
}

func (m *MockIntegrationClient) GetStatsSchema() []statsdata.StatsDims {
	args := m.Called()
	return args.Get(0).([]statsdata.StatsDims)
}

func (m *MockIntegrationClient) ProcessStatsData(aggregatedData *utils.CMap[statsdata.StatsKey, statsdata.StatsData]) error {
	args := m.Called(aggregatedData)
	return args.Error(0)
}

func (m *MockIntegrationClient) Type() integration.IntegrationType {
	return m.integrationType
}
