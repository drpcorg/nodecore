package validations

import (
	"strings"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type ValidationSettingResult int

const (
	Valid ValidationSettingResult = iota + 1
	SettingsError
	FatalSettingError
	UnknownResult
)

const (
	RetryMaxAttempts = 3
)

func validatorExecutor(upstreamId, validationName string, ignoreErrors []string) failsafe.Executor[protocol.ResponseHolder] {
	retryPolicy := retrypolicy.Builder[protocol.ResponseHolder]()

	retryPolicy.WithMaxAttempts(RetryMaxAttempts)
	retryPolicy.WithBackoff(100*time.Millisecond, 500*time.Millisecond)

	retryPolicy.HandleIf(func(result protocol.ResponseHolder, err error) bool {
		isIgnored := lo.SomeBy(ignoreErrors, func(item string) bool {
			return result.HasError() && strings.Contains(result.GetError().Message, item)
		})
		return result.HasError() && !isIgnored
	})

	retryPolicy.OnRetry(func(event failsafe.ExecutionEvent[protocol.ResponseHolder]) {
		log.Error().
			Err(event.LastResult().GetError()).
			Msgf("error during validation '%s' of upstream '%s', iteration - %d", validationName, upstreamId, event.Retries())
	})

	retryPolicy.ReturnLastFailure()

	return failsafe.NewExecutor[protocol.ResponseHolder](retryPolicy.Build())
}

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
	if validators == nil {
		return nil
	}

	return &ValidationProcessor[ValidationSettingResult]{
		validators: validators,
		reduce: func(results []ValidationSettingResult) ValidationSettingResult {
			return lo.Max(results)
		},
	}
}

func NewHealthValidationProcessor(validators []Validator[protocol.AvailabilityStatus]) *ValidationProcessor[protocol.AvailabilityStatus] {
	if validators == nil {
		return nil
	}

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
