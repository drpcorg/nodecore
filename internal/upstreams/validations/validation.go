package validations

import (
	"sync"

	"github.com/samber/lo"
)

type ValidationSettingResult int

const (
	Valid ValidationSettingResult = iota
	SettingsError
	FatalSettingError
)

type SettingsValidator interface {
	Validate() ValidationSettingResult
}

type SettingsValidationProcessor struct {
	validators []SettingsValidator
}

func NewSettingsValidationProcessor(validators []SettingsValidator) *SettingsValidationProcessor {
	return &SettingsValidationProcessor{
		validators: validators,
	}
}

func (s *SettingsValidationProcessor) ValidateUpstreamSettings() ValidationSettingResult {
	results := make([]ValidationSettingResult, len(s.validators))
	var wg sync.WaitGroup
	wg.Add(len(s.validators))

	for i, validator := range s.validators {
		go func(index int) {
			defer wg.Done()
			results[index] = validator.Validate()
		}(i)
	}

	wg.Wait()

	return lo.Max(results)
}
