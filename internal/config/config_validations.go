package config

import (
	"fmt"
	"regexp"

	mapset "github.com/deckarep/golang-set/v2"
)

func (a *AppConfig) validate() error {
	storageNames := make(map[string]string)
	for i, storageConfig := range a.AppStorages {
		storageType, err := storageConfig.validate()
		if err != nil {
			return fmt.Errorf("error during app storage config validation at index %d, cause: %s", i, err.Error())
		}
		// Check for duplicate storage names
		if _, exists := storageNames[storageConfig.Name]; exists {
			return fmt.Errorf("duplicate storage name '%s' at index %d", storageConfig.Name, i)
		}
		storageNames[storageConfig.Name] = storageType
	}
	if a.CacheConfig != nil {
		if err := a.CacheConfig.validate(storageNames); err != nil {
			return err
		}
	}
	if a.IntegrationConfig != nil {
		if err := a.IntegrationConfig.validate(); err != nil {
			return err
		}
	}
	if a.AuthConfig != nil {
		if err := a.AuthConfig.validate(a.IntegrationConfig); err != nil {
			return err
		}
	}
	if a.StatsConfig != nil {
		if err := a.StatsConfig.validate(); err != nil {
			return err
		}
	}
	if err := a.ServerConfig.validate(); err != nil {
		return err
	}

	rateLimitBudgetNames := mapset.NewThreadUnsafeSet[string]()
	if len(a.RateLimit) > 0 {
		for i, budgetConfig := range a.RateLimit {
			if err := budgetConfig.validate(rateLimitBudgetNames, storageNames); err != nil {
				return fmt.Errorf("error during rate limit budget config validation at index %d, cause: %s", i, err.Error())
			}
		}
	}

	if err := a.UpstreamConfig.validate(rateLimitBudgetNames, a.ServerConfig.TorUrl); err != nil {
		return err
	}

	return nil
}

var methodRegex = regexp.MustCompile("^[a-zA-Z0-9_]+$")
