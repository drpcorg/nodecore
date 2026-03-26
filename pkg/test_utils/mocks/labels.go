package mocks

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/mock"
)

type ClientLabelsDetectorMock struct {
	mock.Mock
}

type LabelsDetectorMock struct {
	mock.Mock
}

type LabelsProcessorMock struct {
	mock.Mock

	subManager *utils.SubscriptionManager[lo.Tuple2[string, string]]
}

func NewClientLabelsDetectorMock() *ClientLabelsDetectorMock {
	return &ClientLabelsDetectorMock{}
}

func NewLabelsDetectorMock() *LabelsDetectorMock {
	return &LabelsDetectorMock{}
}

func NewLabelsProcessorMock() *LabelsProcessorMock {
	return &LabelsProcessorMock{
		subManager: utils.NewSubscriptionManager[lo.Tuple2[string, string]]("labels_processor_mock"),
	}
}

func (m *ClientLabelsDetectorMock) NodeTypeRequest() (protocol.RequestHolder, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(protocol.RequestHolder), args.Error(1)
}

func (m *ClientLabelsDetectorMock) ClientVersionAndType(data []byte) (string, string, error) {
	args := m.Called(data)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *LabelsDetectorMock) DetectLabels() map[string]string {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}

	return args.Get(0).(map[string]string)
}

func (m *LabelsProcessorMock) Start() {
	m.Called()
}

func (m *LabelsProcessorMock) Stop() {
	m.Called()
}

func (m *LabelsProcessorMock) Running() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *LabelsProcessorMock) Subscribe(name string) *utils.Subscription[lo.Tuple2[string, string]] {
	m.Called(name)
	return m.subManager.Subscribe(name)
}

func (m *LabelsProcessorMock) Publish(data lo.Tuple2[string, string]) {
	m.subManager.Publish(data)
}
