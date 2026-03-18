package mocks

import (
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/stretchr/testify/mock"
)

type UpstreamStateEventProcessorMock struct {
	mock.Mock
	processorType event_processors.EventProcessorType
}

func NewUpstreamStateEventProcessorMock(processorType event_processors.EventProcessorType) *UpstreamStateEventProcessorMock {
	return &UpstreamStateEventProcessorMock{
		processorType: processorType,
	}
}

func (m *UpstreamStateEventProcessorMock) Start() {
	m.MethodCalled("Start")
}

func (m *UpstreamStateEventProcessorMock) Stop() {
	m.MethodCalled("Stop")
}

func (m *UpstreamStateEventProcessorMock) Running() bool {
	args := m.MethodCalled("Running")
	return args.Bool(0)
}

func (m *UpstreamStateEventProcessorMock) SetEmitter(emitter event_processors.Emitter) {
	m.MethodCalled("SetEmitter", emitter)
}

func (m *UpstreamStateEventProcessorMock) Type() event_processors.EventProcessorType {
	return m.processorType
}

var _ event_processors.UpstreamStateEventProcessor = (*UpstreamStateEventProcessorMock)(nil)
