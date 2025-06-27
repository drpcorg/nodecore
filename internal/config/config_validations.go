package config

import (
	"errors"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/samber/lo"
	"strconv"
	"strings"
	"time"
)

func (a *AppConfig) validate() error {
	if err := a.ServerConfig.validate(); err != nil {
		return err
	}
	if err := a.UpstreamConfig.validate(); err != nil {
		return err
	}
	if a.CacheConfig != nil {
		if err := a.CacheConfig.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *CacheConfig) validate() error {
	connectors := mapset.NewThreadUnsafeSet[string]()
	for i, connector := range c.CacheConnectors {
		if connector.Id == "" {
			return fmt.Errorf("error during cache connectors validation, cause: no connector id under index %d", i)
		}
		if connectors.ContainsOne(connector.Id) {
			return fmt.Errorf("error during cache connectors validation, connector with id %s already exists", connector.Id)
		}
		if err := connector.validate(); err != nil {
			return fmt.Errorf("error during cache connector %s validation, cause: %s", connector.Id, err.Error())
		}
		connectors.Add(connector.Id)
	}
	policies := mapset.NewThreadUnsafeSet[string]()
	for i, policy := range c.CachePolicies {
		if policy.Id == "" {
			return fmt.Errorf("error during cache policies validation, cause: no policy id under index %d", i)
		}
		if policies.ContainsOne(policy.Id) {
			return fmt.Errorf("error during cache policies validation, policy with id %s already exists", policy.Id)
		}
		if err := policy.validate(connectors); err != nil {
			return fmt.Errorf("error during cache policy %s validation, cause: %s", policy.Id, err.Error())
		}
		policies.Add(policy.Id)
	}
	return nil
}

func (c *CacheConnectorConfig) validate() error {
	if err := c.Driver.validate(); err != nil {
		return err
	}
	if c.Memory.MaxItems <= 0 {
		return errors.New("memory max items must be > 0")
	}
	if c.Memory.ExpiredRemoveInterval == 0 {
		return errors.New("expired remove interval must be > 0")
	}

	return nil
}

func (d CacheConnectorDriver) validate() error {
	switch d {
	case Memory, Redis:
	default:
		return fmt.Errorf("invalid cache driver - %s", d)
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
		return fmt.Errorf("there is no such connector - %s", p.Connector)
	}
	return nil
}

func (f FinalizationType) validate() error {
	switch f {
	case Finalized, None:
	default:
		return fmt.Errorf("invalid finalization type - %s", f)
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
			return fmt.Errorf("chain %s is not supported", chainStr)
		}
	}
	return nil
}

func (u *UpstreamConfig) validate() error {
	if err := u.ScorePolicyConfig.validate(); err != nil {
		return fmt.Errorf("error during score policy config, cause: %s", err.Error())
	}

	for chain, chainDefault := range u.ChainDefaults {
		if !chains.IsSupported(chain) {
			return fmt.Errorf("error during chain defaults validation, cause: not supported chain %s", chain)
		}
		if err := chainDefault.validate(); err != nil {
			return fmt.Errorf("error during chain %s defaults validation, cause: %s", chain, err.Error())
		}
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return fmt.Errorf("error during failsafe validation of upstream-conifg: %s", err.Error())
	}

	if len(u.Upstreams) == 0 {
		return errors.New("there must be at least one upstream in the config")
	}

	idSet := mapset.NewThreadUnsafeSet[string]()
	for i, upstream := range u.Upstreams {
		if upstream.Id == "" {
			return fmt.Errorf("error during upstream validation, cause: no upstream id under index %d", i)
		}
		if idSet.Contains(upstream.Id) {
			return fmt.Errorf("error during upstream validation, cause: upstream with id %s already exists", upstream.Id)
		}
		if err := upstream.validate(); err != nil {
			return fmt.Errorf("error during upstream %s validation, cause: %s", upstream.Id, err.Error())
		}
		idSet.Add(upstream.Id)
	}

	return nil
}

func (s *ScorePolicyConfig) validate() error {
	if s.CalculationInterval <= 0 {
		return errors.New("the calculation interval can't be less than 0")
	}
	if s.CalculationFunction != "" && s.CalculationFunctionFilePath != "" {
		return errors.New("one setting must be specified - either 'calculation-function' or 'calculation-function-file-path'")
	}
	_, err := s.compileFunc()
	if err != nil {
		return fmt.Errorf("couldn't read a ts script, %s", err.Error())
	}
	return nil
}

func (f *FailsafeConfig) validate() error {
	if f.HedgeConfig != nil {
		if err := f.HedgeConfig.validate(); err != nil {
			return fmt.Errorf("hedge config validation error - %s", err.Error())
		}
	}
	if f.RetryConfig != nil {
		if err := f.RetryConfig.validate(); err != nil {
			return fmt.Errorf("retry config validation error - %s", err.Error())
		}
	}
	return nil
}

func (r *RetryConfig) validate() error {
	if r.Attempts < 1 {
		return errors.New("the number of attempts can't be less than 1")
	}
	if r.Delay <= 0 {
		return errors.New("the retry delay can't be less than 0")
	}
	if r.MaxDelay <= 0 {
		return errors.New("the retry max delay can't be less than 0")
	}
	if r.Jitter <= 0 {
		return errors.New("the retry jitter can't be 0")
	}
	if r.Delay > r.MaxDelay {
		return errors.New("the retry delay can't be greater than the retry max delay")
	}
	return nil
}

func (h *HedgeConfig) validate() error {
	if h.Count <= 0 {
		return errors.New("the number of hedges can't be less than 1")
	}
	if h.Delay.Milliseconds() < 50 {
		return errors.New("the hedge delay can't be less than 50ms")
	}
	return nil
}

func (s *ServerConfig) validate() error {
	return nil
}

func (u *Upstream) validate() error {
	if !chains.IsSupported(u.ChainName) {
		return fmt.Errorf("not supported chain %s", u.ChainName)
	}

	if len(u.Connectors) == 0 {
		return fmt.Errorf("there must be at least one upstream connector")
	}

	connectorTypeSet := mapset.NewThreadUnsafeSet[ApiConnectorType]()
	for _, connector := range u.Connectors {
		if connectorTypeSet.Contains(connector.Type) {
			return fmt.Errorf("there can be only one connector of type %s", connector.Type)
		}
		if err := connector.validate(); err != nil {
			return err
		}
		connectorTypeSet.Add(connector.Type)
	}

	if err := u.HeadConnector.validate(); err != nil {
		return fmt.Errorf("invalid head connector - %s", u.HeadConnector)
	}

	if !connectorTypeSet.Contains(u.HeadConnector) {
		return fmt.Errorf("there is no %s connector for head", u.HeadConnector)
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return err
	}

	return nil
}

func (c *ChainDefaults) validate() error {
	return nil
}

func (c *ApiConnectorConfig) validate() error {
	if err := c.Type.validate(); err != nil {
		return err
	}

	if c.Url == "" {
		return fmt.Errorf("url must be specified for connector %s", c.Type)
	}

	return nil
}

func (t ApiConnectorType) validate() error {
	switch t {
	case Grpc, JsonRpc, Rest, Ws:
	default:
		return fmt.Errorf("invalid connector type - %s", t)
	}
	return nil
}
