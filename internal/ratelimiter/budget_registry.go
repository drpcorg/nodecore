package ratelimiter

import "github.com/drpcorg/nodecore/internal/config"

type RateLimitBudgetRegistry struct {
	rateLimitBudgets map[string]*RateLimitBudget
}

func NewRateLimitBudgetRegistry(cfg []config.RateLimitBudgetConfig) *RateLimitBudgetRegistry {
	defaultEngine := NewRateLimitMemoryEngine()
	rateLimitBudgets := make(map[string]*RateLimitBudget)
	for _, budgetConfig := range cfg {
		for _, budget := range budgetConfig.Budgets {
			rateLimitBudget := NewRateLimitBudget(&budget, defaultEngine)
			rateLimitBudgets[budget.Name] = rateLimitBudget
		}
	}
	return &RateLimitBudgetRegistry{
		rateLimitBudgets: rateLimitBudgets,
	}
}

func (r *RateLimitBudgetRegistry) Get(name string) (*RateLimitBudget, bool) {
	budget, ok := r.rateLimitBudgets[name]
	return budget, ok
}
