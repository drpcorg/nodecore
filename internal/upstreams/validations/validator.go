package validations

import (
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/samber/lo"
)

type ValidationSettingResult int

const (
	Valid ValidationSettingResult = iota + 1
	SettingsError
	FatalSettingError
)

type Validator[R any] interface {
	Validate() R
}

type HealthValidator interface {
	Validator[protocol.AvailabilityStatus]
}

type SettingsValidator interface {
	Validator[ValidationSettingResult]
}

type ValidationProcessor[R any] struct {
	validators []Validator[R]
	reduce     func([]R) R
}

func NewSettingsValidationProcessor(validators []Validator[ValidationSettingResult]) *ValidationProcessor[ValidationSettingResult] {
	return &ValidationProcessor[ValidationSettingResult]{
		validators: validators,
		reduce: func(results []ValidationSettingResult) ValidationSettingResult {
			return lo.Max(results)
		},
	}
}

func NewHealthValidationProcessor(validators []Validator[protocol.AvailabilityStatus]) *ValidationProcessor[protocol.AvailabilityStatus] {
	return &ValidationProcessor[protocol.AvailabilityStatus]{
		validators: validators,
		reduce: func(statuses []protocol.AvailabilityStatus) protocol.AvailabilityStatus {
			return lo.Max(statuses)
		},
	}
}

func (s *ValidationProcessor[R]) Validate() R {
	results := make([]R, len(s.validators))
	var wg sync.WaitGroup
	wg.Add(len(s.validators))

	for i, validator := range s.validators {
		go func(index int) {
			defer wg.Done()
			results[index] = validator.Validate()
		}(i)
	}

	wg.Wait()

	return s.reduce(results)
}
