package mocks

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type MethodsDetectorMock struct {
	mock.Mock
}

func NewMethodsDetectorMock() *MethodsDetectorMock {
	return &MethodsDetectorMock{}
}

func (m *MethodsDetectorMock) DetectUnsupported(ctx context.Context) mapset.Set[string] {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(mapset.Set[string])
}

type MethodsProcessorMock struct {
	mock.Mock

	subManager *utils.SubscriptionManager[mapset.Set[string]]
}

func NewMethodsProcessorMock() *MethodsProcessorMock {
	return &MethodsProcessorMock{
		subManager: utils.NewSubscriptionManager[mapset.Set[string]]("methods_processor_mock"),
	}
}

func (m *MethodsProcessorMock) Start() {
	m.Called()
}

func (m *MethodsProcessorMock) Stop() {
	m.Called()
}

func (m *MethodsProcessorMock) Running() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MethodsProcessorMock) Subscribe(name string) *utils.Subscription[mapset.Set[string]] {
	m.Called(name)
	return m.subManager.Subscribe(name)
}

func (m *MethodsProcessorMock) Publish(unsupported mapset.Set[string]) {
	m.subManager.Publish(unsupported)
}
