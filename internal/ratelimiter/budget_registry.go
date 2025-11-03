package ratelimiter

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/storages"
)

type RateLimitBudgetRegistry struct {
	rateLimitBudgets map[string]*RateLimitBudget
}

func storageToEngine(name string, storage storages.Storage) (RateLimitEngine, error) {
	switch tstore := storage.(type) {
	case *storages.RedisStorage:
		return NewRateLimitRedisEngine(name, tstore.Redis), nil
	default:
		return nil, fmt.Errorf("unsupported storage type %T", storage)
	}
}

func NewRateLimitBudgetRegistry(cfg []config.RateLimitBudgetsConfig, storageRegistry *storages.StorageRegistry) (*RateLimitBudgetRegistry, error) {
	var defaultEngine RateLimitEngine = NewRateLimitMemoryEngine()
	rateLimitBudgets := make(map[string]*RateLimitBudget)
	for _, budgetConfig := range cfg {
		if budgetConfig.DefaultStorage == "" {
			defaultEngine = NewRateLimitMemoryEngine()
		} else {
			storage, ok := storageRegistry.Get(budgetConfig.DefaultStorage)
			if !ok {
				return nil, fmt.Errorf("default storage %s not found", budgetConfig.DefaultStorage)
			}
			engine, err := storageToEngine(budgetConfig.DefaultStorage, storage)
			if err != nil {
				return nil, fmt.Errorf("couldn't create a rate limit engine for storage %s, reason - %s", budgetConfig.DefaultStorage, err.Error())
			}
			defaultEngine = engine
		}

		for _, budget := range budgetConfig.Budgets {
			budgetEngine := defaultEngine
			if budget.Storage != "" {
				var ok bool
				storage, ok := storageRegistry.Get(budget.Storage)
				if !ok {
					return nil, fmt.Errorf("storage %s not found", budget.Storage)
				}
				engine, err := storageToEngine(budget.Storage, storage)
				if err != nil {
					return nil, fmt.Errorf("couldn't create a rate limit engine for storage %s, reason - %s", budget.Storage, err.Error())
				}
				budgetEngine = engine
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
