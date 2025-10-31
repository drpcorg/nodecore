package ratelimiter

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
)

type RateLimitBudgetRegistry struct {
	rateLimitBudgets map[string]*RateLimitBudget
}

func NewRateLimitBudgetRegistry(cfg []config.RateLimitBudgetConfig, engineRegistry *RateLimitEngineRegistry) (*RateLimitBudgetRegistry, error) {
	var defaultEngine RateLimitEngine = NewRateLimitMemoryEngine()
	rateLimitBudgets := make(map[string]*RateLimitBudget)
	for _, budgetConfig := range cfg {
		if budgetConfig.DefaultEngine == "" {
			defaultEngine = NewRateLimitMemoryEngine()
		} else {
			budgetEngine, ok := engineRegistry.Get(budgetConfig.DefaultEngine)
			if !ok {
				return nil, fmt.Errorf("default engine %s not found", budgetConfig.DefaultEngine)
			}
			defaultEngine = budgetEngine
		}

		for _, budget := range budgetConfig.Budgets {
			budgetEngine := defaultEngine
			if budget.Engine != "" {
				var ok bool
				budgetEngine, ok = engineRegistry.Get(budget.Engine)
				if !ok {
					return nil, fmt.Errorf("engine %s not found", budget.Engine)
				}
			}
			rateLimitBudget := NewRateLimitBudget(&budget, budgetEngine)
			rateLimitBudgets[budget.Name] = rateLimitBudget
		}
	}
	return &RateLimitBudgetRegistry{
		rateLimitBudgets: rateLimitBudgets,
	}, nil
}

func (r *RateLimitBudgetRegistry) Get(name string) (*RateLimitBudget, bool) {
	budget, ok := r.rateLimitBudgets[name]
	return budget, ok
}
