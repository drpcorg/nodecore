package config

import (
	"errors"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
)

func (r *RateLimitBudgetsConfig) validate(budgetNames mapset.Set[string], storageNames map[string]string) error {
	for i, budget := range r.Budgets {
		if budget.Name == "" {
			return fmt.Errorf("rate limit budget name cannot be empty at index %d", i)
		}
		if budgetNames.Contains(budget.Name) {
			return fmt.Errorf("duplicate budget name '%s'", budget.Name)
		}
		if err := budget.validate(storageNames); err != nil {
			return fmt.Errorf("error during rate limit budget '%s' validation, cause: %s", budget.Name, err.Error())
		}
		budgetNames.Add(budget.Name)
	}
	return nil
}

func (r *RateLimitBudget) validate(storageNames map[string]string) error {
	if r.Name == "" {
		return errors.New("rate limit budget name cannot be empty")
	}

	if r.Config != nil {
		if err := r.Config.validate(); err != nil {
			return fmt.Errorf("rate limit budget '%s' validation error: %s", r.Name, err.Error())
		}
	}

	// Validate storage reference if specified
	if r.Storage != "" {
		storage, ok := storageNames[r.Storage]
		if !ok {
			return fmt.Errorf("rate limit budget '%s' references non-existent storage '%s'", r.Name, r.Storage)
		}
		if storage != "redis" {
			return fmt.Errorf("rate limit budget '%s' storage '%s' is not a redis storage (type: %s)", r.Name, r.Storage, storage)
		}
	}

	return nil
}

func (r *RateLimiterConfig) validate() error {
	for _, rule := range r.Rules {
		if rule.Method == "" && rule.Pattern == "" {
			return errors.New("the method or pattern must be specified")
		}
		if rule.Method != "" && rule.Pattern != "" {
			return errors.New("the method and pattern can't be specified at the same time")
		}
		if rule.Method != "" && !methodRegex.MatchString(rule.Method) {
			return errors.New("the method must be a valid method name, you can't use regex, otherwise use pattern: 'pattern' instead of method")
		}
		if rule.Period <= 0 {
			return errors.New("the period must be greater than 0")
		}
		if rule.Requests < 1 {
			return errors.New("the requests must be greater than 0")
		}
	}
	return nil
}

func (r *RateLimitAutoTuneConfig) validate() error {
	if !r.Enabled {
		return nil
	}

	if r.Period <= 0 {
		return errors.New("period must be greater than 0 when auto-tune is enabled")
	}

	if r.ErrorRateThreshold < 0 || r.ErrorRateThreshold > 1 {
		return errors.New("error-threshold must be between 0 and 1")
	}

	if r.InitRateLimit <= 0 {
		return errors.New("init-rate-limit must be greater than 0 when auto-tune is enabled")
	}

	if r.InitRateLimitPeriod <= 0 {
		return errors.New("init-rate-limit-period must be greater than 0 when auto-tune is enabled")
	}

	if r.InitRateLimitPeriod > r.Period {
		return errors.New("init-rate-limit-period must be less than or equal to the period when auto-tune is enabled")
	}

	return nil
}
