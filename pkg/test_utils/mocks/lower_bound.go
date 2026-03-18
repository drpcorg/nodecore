package mocks

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type LowerBoundDetectorMock struct {
	mock.Mock
}

func NewLowerBoundDetectorMock() *LowerBoundDetectorMock {
	return &LowerBoundDetectorMock{}
}

func (m *LowerBoundDetectorMock) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	args := m.Called()
	var bounds []protocol.LowerBoundData
	if args.Get(0) != nil {
		bounds = args.Get(0).([]protocol.LowerBoundData)
	}
	return bounds, args.Error(1)
}

func (m *LowerBoundDetectorMock) SupportedTypes() []protocol.LowerBoundType {
	args := m.Called()
	return args.Get(0).([]protocol.LowerBoundType)
}

func (m *LowerBoundDetectorMock) Period() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}

type LowerBoundProcessorMock struct {
	mock.Mock

	subManager *utils.SubscriptionManager[protocol.LowerBoundData]
}

func NewLowerBoundProcessorMock() *LowerBoundProcessorMock {
	return &LowerBoundProcessorMock{
		subManager: utils.NewSubscriptionManager[protocol.LowerBoundData]("lower_bound_processor_mock"),
	}
}

func (m *LowerBoundProcessorMock) Start() {
	m.Called()
}

func (m *LowerBoundProcessorMock) Stop() {
	m.Called()
}

func (m *LowerBoundProcessorMock) Running() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *LowerBoundProcessorMock) Subscribe(name string) *utils.Subscription[protocol.LowerBoundData] {
	m.Called(name)
	return m.subManager.Subscribe(name)
}

func (m *LowerBoundProcessorMock) PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64 {
	args := m.Called(boundType, timeOffset)
	return args.Get(0).(int64)
}

func (m *LowerBoundProcessorMock) Publish(data protocol.LowerBoundData) {
	m.subManager.Publish(data)
}
