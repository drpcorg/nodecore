package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/mock"
)

type MockAuthProcessor struct {
	mock.Mock
}

func NewMockAuthProcessor() *MockAuthProcessor {
	return &MockAuthProcessor{}
}

func (m *MockAuthProcessor) GetKeyValue(payload auth.AuthPayload) string {
	args := m.Called(payload)
	return args.String(0)
}

func (m *MockAuthProcessor) Authenticate(ctx context.Context, payload auth.AuthPayload) error {
	args := m.Called(ctx, payload)
	return args.Error(0)
}

func (m *MockAuthProcessor) PreKeyValidate(ctx context.Context, payload auth.AuthPayload) ([]string, error) {
	args := m.Called(ctx, payload)
	var cors []string
	if args.Get(0) == nil {
		cors = nil
	} else {
		cors = args.Get(0).([]string)
	}
	return cors, args.Error(1)
}

func (m *MockAuthProcessor) PostKeyValidate(ctx context.Context, payload auth.AuthPayload, request protocol.RequestHolder) error {
	args := m.Called(ctx, payload, request)
	return args.Error(0)
}
