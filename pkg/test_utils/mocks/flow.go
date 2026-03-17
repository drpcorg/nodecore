package mocks

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/mock"
)

type MockStrategy struct {
	mock.Mock
}

func (m *MockStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	args := m.Called(request)
	return args.Get(0).(string), args.Error(1)
}

func NewMockStrategy() *MockStrategy {
	return &MockStrategy{}
}
