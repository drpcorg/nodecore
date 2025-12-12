package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

func (c *CacheConfig) validate(storageNames map[string]string) error {
	connectors := mapset.NewThreadUnsafeSet[string]()
	for i, connector := range c.CacheConnectors {
		if connector.Id == "" {
			return fmt.Errorf("error during cache connectors validation, cause: no connector id under index %d", i)
		}
		if connectors.ContainsOne(connector.Id) {
			return fmt.Errorf("error during cache connectors validation, connector with id '%s' already exists", connector.Id)
		}
		if err := connector.validate(storageNames); err != nil {
			return fmt.Errorf("error during cache connector '%s' validation, cause: %s", connector.Id, err.Error())
		}
		connectors.Add(connector.Id)
	}
	policies := mapset.NewThreadUnsafeSet[string]()
	for i, policy := range c.CachePolicies {
		if policy.Id == "" {
			return fmt.Errorf("error during cache policies validation, cause: no policy id under index %d", i)
		}
		if policies.ContainsOne(policy.Id) {
			return fmt.Errorf("error during cache policies validation, policy with id '%s' already exists", policy.Id)
		}
		if err := policy.validate(connectors); err != nil {
			return fmt.Errorf("error during cache policy '%s' validation, cause: %s", policy.Id, err.Error())
		}
		policies.Add(policy.Id)
	}
	return nil
}

func (c *CacheConnectorConfig) validate(storageNames map[string]string) error {
	if err := c.Driver.validate(); err != nil {
		return err
	}
	if c.Memory != nil {
		if err := c.Memory.validate(); err != nil {
			return err
		}
	}
	if c.Redis != nil {
		storage, ok := storageNames[c.Redis.StorageName]
		if !ok {
			return fmt.Errorf("redis storage name '%s' not found", c.Redis.StorageName)
		}
		if storage != "redis" {
			return fmt.Errorf("redis storage name '%s' is not a redis storage", c.Redis.StorageName)
		}
	}
	if c.Postgres != nil {
		if err := c.Postgres.validate(storageNames); err != nil {
			return err
		}
	}

	return nil
}

func (p *PostgresCacheConnectorConfig) validate(storageNames map[string]string) error {
	storage, ok := storageNames[p.StorageName]
	if !ok {
		return fmt.Errorf("postgres storage name '%s' not found", p.StorageName)
	}
	if storage != "postgres" {
		return fmt.Errorf("postgres storage name '%s' is not a postgres storage", p.StorageName)
	}

	if p.QueryTimeout != nil && *p.QueryTimeout < 0 {
		return errors.New("query-timeout must be greater than or equal to 0")
	}
	if p.ExpiredRemoveInterval <= 0 {
		return errors.New("expired remove interval must be > 0")
	}

	return nil
}

func (m *MemoryCacheConnectorConfig) validate() error {
	if m.MaxItems <= 0 {
		return errors.New("memory max items must be > 0")
	}
	if m.ExpiredRemoveInterval <= 0 {
		return errors.New("expired remove interval must be > 0")
	}

	return nil
}

func (d CacheConnectorDriver) validate() error {
	switch d {
	case Memory, Redis, Postgres:
	default:
		return fmt.Errorf("invalid cache driver - '%s'", d)
	}
	return nil
}

func (f FinalizationType) validate() error {
	switch f {
	case Finalized, None:
	default:
		return fmt.Errorf("invalid finalization type - '%s'", f)
	}
	return nil
}

func (p *CachePolicyConfig) validate(connectors mapset.Set[string]) error {
	if err := validatePolicyChain(p.Chain); err != nil {
		return err
	}
	if err := validateSize(p.ObjectMaxSize); err != nil {
		return err
	}
	if p.Method == "" {
		return errors.New("empty method setting")
	}
	if p.Connector == "" {
		return errors.New("empty connector")
	}
	if err := p.FinalizationType.validate(); err != nil {
		return err
	}
	if _, err := time.ParseDuration(p.TTL); err != nil {
		return err
	}
	if !connectors.ContainsOne(p.Connector) {
		return fmt.Errorf("there is no such connector - '%s'", p.Connector)
	}
	return nil
}

func validateSize(size string) error {
	var maxSize string

	if strings.HasSuffix(size, "MB") {
		maxSize = strings.TrimSuffix(size, "MB")
	} else if strings.HasSuffix(size, "KB") {
		maxSize = strings.TrimSuffix(size, "KB")
	} else {
		return errors.New("size must be in KB or MB")
	}

	maxSizeInt, err := strconv.ParseInt(strings.TrimSpace(maxSize), 10, 64)
	if err != nil {
		return fmt.Errorf("couldn't parse size - %s", err.Error())
	}
	if maxSizeInt <= 0 {
		return errors.New("size must be > 0")
	}

	return nil
}

func validatePolicyChain(chain string) error {
	if chain == "" {
		return errors.New("empty chain setting")
	}
	if chain == "*" {
		return nil
	}
	cacheChainsStr := lo.Map(strings.Split(chain, "|"), func(item string, index int) string {
		return strings.TrimSpace(item)
	})
	for _, chainStr := range cacheChainsStr {
		if !chains.IsSupported(chainStr) {
			return fmt.Errorf("chain '%s' is not supported", chainStr)
		}
	}
	return nil
}
