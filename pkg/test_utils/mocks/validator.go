package mocks

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/stretchr/testify/mock"
)

type SettingsValidatorMock struct {
	mock.Mock
}

func NewSettingsValidatorMock() *SettingsValidatorMock {
	return &SettingsValidatorMock{}
}

func (s *SettingsValidatorMock) Validate(ctx context.Context) validations.ValidationSettingResult {
	return s.Called(ctx).Get(0).(validations.ValidationSettingResult)
}

type HealthValidatorMock struct {
	mock.Mock
}

func NewHealthValidatorMock() *HealthValidatorMock {
	return &HealthValidatorMock{}
}

func (h *HealthValidatorMock) Validate(ctx context.Context) protocol.AvailabilityStatus {
	return h.Called(ctx).Get(0).(protocol.AvailabilityStatus)
}
