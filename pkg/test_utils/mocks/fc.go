package mocks

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/mock"
)

type MockForkChoice struct {
	mock.Mock
}

func (m *MockForkChoice) Choose(upstreamId string, event *protocol.HeadUpstreamEvent) (bool, protocol.Block) {
	args := m.Called(upstreamId, event)
	return args.Get(0).(bool), args.Get(1).(protocol.Block)
}

func NewMockForkChoice() *MockForkChoice {
	return &MockForkChoice{}
}
