package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

func (a *AppConfig) validate() error {
	storageNames := make(map[string]string)
	for i, storageConfig := range a.AppStorages {
		storageType, err := storageConfig.validate()
		if err != nil {
			return fmt.Errorf("error during app storage config validation at index %d, cause: %s", i, err.Error())
		}
		storageNames[storageConfig.Name] = storageType
	}
	if a.CacheConfig != nil {
		if err := a.CacheConfig.validate(storageNames); err != nil {
			return err
		}
	}
	if a.AuthConfig != nil {
		if err := a.AuthConfig.validate(); err != nil {
			return err
		}
	}
	if err := a.ServerConfig.validate(); err != nil {
		return err
	}

	rateLimitBudgetNames := mapset.NewThreadUnsafeSet[string]()
	if len(a.RateLimitBudgets) > 0 {
		for i, budgetConfig := range a.RateLimitBudgets {
			if err := budgetConfig.validate(); err != nil {
				return fmt.Errorf("error during rate limit budget config validation at index %d, cause: %s", i, err.Error())
			}
			for _, budget := range budgetConfig.Budgets {
<<<<<<< HEAD
				if rateLimitBudgetNames.Contains(budget.Name) {
					return fmt.Errorf("error during rate limit budget config validation, cause: duplicate rate limit budget name '%s'", budget.Name)
				}
=======
>>>>>>> e0f10bd (add rate limit shared budgets)
				rateLimitBudgetNames.Add(budget.Name)
			}
		}
	}

	if err := a.UpstreamConfig.validate(rateLimitBudgetNames); err != nil {
		return err
	}

	return nil
}

func (a *AppStorageConfig) validate() (string, error) {
	if a.Name == "" {
		return "", errors.New("app storage name cannot be empty")
	}
	if a.Redis != nil {
		if err := a.Redis.validate(); err != nil {
			return "", fmt.Errorf("error during redis storage config validation, cause: %s", err.Error())
		}
		return "redis", nil
	}
	if a.Postgres != nil {
		// Postgres validation is minimal - just check URL exists
		if a.Postgres.Url == "" {
			return "", errors.New("postgres url cannot be empty")
		}
		return "postgres", nil
	}
	return "", errors.New("storage must have either redis or postgres configuration")
}

func (a *AuthConfig) validate() error {
	if !a.Enabled {
		return nil
	}
	if a.RequestStrategyConfig != nil {
		if err := a.RequestStrategyConfig.validate(); err != nil {
			return err
		}
	}
	if len(a.KeyConfigs) > 0 {
		keyIds := mapset.NewThreadUnsafeSet[string]()
		keys := mapset.NewThreadUnsafeSet[string]()
		for i, keyConfig := range a.KeyConfigs {
			if keyConfig.Id == "" {
				return fmt.Errorf("error during key config validation, cause: no key id under index %d", i)
			}
			if keyIds.ContainsOne(keyConfig.Id) {
				return fmt.Errorf("error during key config validation, key with id '%s' already exists", keyConfig.Id)
			}
			if keyConfig.LocalKeyConfig != nil && keys.ContainsOne(keyConfig.LocalKeyConfig.Key) {
				return fmt.Errorf("error during key config validation, local key '%s' already exists", keyConfig.LocalKeyConfig.Key)
			}
			if err := keyConfig.validate(); err != nil {
				return fmt.Errorf("error during '%s' key config validation, cause: %s", keyConfig.Id, err.Error())
			}
			keyIds.Add(keyConfig.Id)
			if keyConfig.LocalKeyConfig != nil {
				keys.Add(keyConfig.LocalKeyConfig.Key)
			}
		}
	}
	return nil
}

func (k *KeyConfig) validate() error {
	if err := k.Type.validate(); err != nil {
		return err
	}
	switch k.Type {
	case Local:
		if k.LocalKeyConfig == nil {
			return fmt.Errorf("specified '%s' key management rule type but there are no its settings", k.Type)
		}
		if err := k.LocalKeyConfig.validate(); err != nil {
			return fmt.Errorf("error during '%s' key config validation, cause: %s", k.Id, err.Error())
		}
	}
	return nil
}

func (l *LocalKeyConfig) validate() error {
	if l.Key == "" {
		return errors.New("'key' field is empty")
	}
	return nil
}

func (s KeyType) validate() error {
	switch s {
	case Local:
	default:
		return fmt.Errorf("invalid settings strategy type - '%s'", s)
	}
	return nil
}

func (r *RequestStrategyConfig) validate() error {
	if err := r.Type.validate(); err != nil {
		return err
	}
	switch r.Type {
	case Token:
		if r.TokenRequestStrategyConfig == nil {
			return fmt.Errorf("specified '%s' request strategy type but there are no its settings", r.Type)
		}
		if err := r.TokenRequestStrategyConfig.validate(); err != nil {
			return fmt.Errorf("error during '%s' request strategy validation, cause: %s", r.Type, err.Error())
		}
	case Jwt:
		if r.JwtRequestStrategyConfig == nil {
			return fmt.Errorf("specified '%s' request strategy type but there are no its settings", r.Type)
		}
		if err := r.JwtRequestStrategyConfig.validate(); err != nil {
			return fmt.Errorf("error during '%s' request strategy validation, cause: %s", r.Type, err.Error())
		}
	}

	return nil
}

func (j *JwtRequestStrategyConfig) validate() error {
	if j.PublicKey == "" {
		return errors.New("there is no the public key path")
	}
	return nil
}

func (t *TokenRequestStrategyConfig) validate() error {
	if t.Value == "" {
		return errors.New("there is no secret value")
	}
	return nil
}

func (r RequestStrategyType) validate() error {
	switch r {
	case Token, Jwt:
	default:
		return fmt.Errorf("invalid request strategy type - '%s'", r)
	}
	return nil
}

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

func (r *RedisStorageConfig) validate() error {
	if r.FullUrl == "" && r.Address == "" {
		return errors.New("either 'address' or 'full_url' must be specified")
	}
	if r.Timeouts != nil {
		if r.Timeouts.ReadTimeout != nil && *r.Timeouts.ReadTimeout < 0 {
			return errors.New("read timeout cannot be negative")
		}
		if r.Timeouts.WriteTimeout != nil && *r.Timeouts.WriteTimeout < 0 {
			return errors.New("write timeout cannot be negative")
		}
		if r.Timeouts.ConnectTimeout != nil && *r.Timeouts.ConnectTimeout < 0 {
			return errors.New("connect timeout cannot be negative")
		}
	}
	if r.Pool != nil {
		if r.Pool.Size < 0 {
			return errors.New("pool size cannot be negative")
		}
		if r.Pool.PoolTimeout != nil && *r.Pool.PoolTimeout < 0 {
			return errors.New("pool timeout cannot be negative")
		}
		if r.Pool.MinIdleConns < 0 {
			return errors.New("pool min idle connections cannot be negative")
		}
		if r.Pool.MaxIdleConns < 0 {
			return errors.New("pool max idle connections cannot be negative")
		}
		if r.Pool.MinIdleConns > r.Pool.MaxIdleConns {
			return errors.New("pool min idle connections cannot be greater than pool max idle connections")
		}
		if r.Pool.MaxActiveConns < 0 {
			return errors.New("pool max connections cannot be negative")
		}
		if r.Pool.ConnMaxLifeTime != nil && *r.Pool.ConnMaxLifeTime < 0 {
			return errors.New("pool conn max life time cannot be negative")
		}
		if r.Pool.ConnMaxIdleTime != nil && *r.Pool.ConnMaxIdleTime < 0 {
			return errors.New("pool conn max idle time cannot be negative")
		}
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

func (f FinalizationType) validate() error {
	switch f {
	case Finalized, None:
	default:
		return fmt.Errorf("invalid finalization type - '%s'", f)
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

var methodRegex = regexp.MustCompile("^[a-zA-Z0-9_]+$")

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

func (u *UpstreamConfig) validate(rateLimitBudgetNames mapset.Set[string]) error {
	if err := u.ScorePolicyConfig.validate(); err != nil {
		return fmt.Errorf("error during score policy config validation, cause: %s", err.Error())
	}

	for chain, chainDefault := range u.ChainDefaults {
		if !chains.IsSupported(chain) {
			return fmt.Errorf("error during chain defaults validation, cause: not supported chain %s", chain)
		}
		if err := chainDefault.validate(); err != nil {
			return fmt.Errorf("error during chain '%s' defaults validation, cause: %s", chain, err.Error())
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
			return fmt.Errorf("error during upstream validation, cause: upstream with id '%s' already exists", upstream.Id)
		}
		if err := upstream.validate(); err != nil {
			return fmt.Errorf("error during upstream '%s' validation, cause: %s", upstream.Id, err.Error())
		}
		// Validate rate limit budget reference
		if upstream.RateLimitBudget != "" && !rateLimitBudgetNames.Contains(upstream.RateLimitBudget) {
			return fmt.Errorf("upstream '%s' references non-existent rate limit budget '%s'", upstream.Id, upstream.RateLimitBudget)
		}
		idSet.Add(upstream.Id)
	}

	return nil
}

func (s *ScorePolicyConfig) validate() error {
	if s.CalculationInterval <= 0 {
		return errors.New("the calculation interval can't be less than 0")
	}
	if s.CalculationFunctionName != "" && s.CalculationFunctionFilePath != "" {
		return errors.New("one setting must be specified - either 'calculation-function' or 'calculation-function-file-path'")
	}
	if s.CalculationFunctionName != "" {
		_, ok := defaultRatingFunctions[s.CalculationFunctionName]
		if !ok {
			return fmt.Errorf("'%s' default function doesn't exist", s.CalculationFunctionName)
		}
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
	if r.MaxDelay != nil && *r.MaxDelay <= 0 {
		return errors.New("the retry max delay can't be less than 0")
	}
	if r.Jitter != nil && *r.Jitter <= 0 {
		return errors.New("the retry jitter can't be 0")
	}
	if r.MaxDelay != nil && r.Delay > *r.MaxDelay {
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
	if s.Port < 0 {
		return fmt.Errorf("incorrect server port - %d", s.Port)
	}
	if s.MetricsPort < 0 {
		return fmt.Errorf("incorrect metrics port - %d", s.MetricsPort)
	}
	if s.PprofPort < 0 {
		return fmt.Errorf("incorrect pprof port - %d", s.PprofPort)
	}

	ports := mapset.NewThreadUnsafeSet[int](s.Port)
	if ports.Contains(s.MetricsPort) && s.MetricsPort != 0 {
		return fmt.Errorf("metrics port %d is already in use", s.MetricsPort)
	}
	ports.Add(s.MetricsPort)
	if ports.Contains(s.PprofPort) && s.PprofPort != 0 {
		return fmt.Errorf("pprof port %d is already in use", s.PprofPort)
	}

	if err := s.TlsConfig.validate(); err != nil {
		return fmt.Errorf("tls config validation error - %s", err.Error())
	}

	if err := s.PyroscopeConfig.validate(); err != nil {
		return err
	}

	return nil
}

func (t *TlsConfig) validate() error {
	if t.Enabled {
		if t.Certificate == "" {
			return errors.New("the tls certificate can't be empty")
		}
		if t.Key == "" {
			return errors.New("the tls certificate key can't be empty")
		}
	}
	return nil
}

func (p *PyroscopeConfig) validate() error {
	if p.Enabled {
		if p.Url == "" {
			return errors.New("pyroscope is enabled, url must be specified")
		}
		if p.Username == "" {
			return errors.New("pyroscope is enabled, username must be specified")
		}
		if p.Password == "" {
			return errors.New("pyroscope is enabled, password must be specified")
		}
	}

	return nil
}

func (u *Upstream) validate() error {
	if !chains.IsSupported(u.ChainName) {
		return fmt.Errorf("not supported chain '%s'", u.ChainName)
	}

	if len(u.Connectors) == 0 {
		return fmt.Errorf("there must be at least one upstream connector")
	}

	if u.RateLimit != nil {
		if err := u.RateLimit.validate(); err != nil {
			return fmt.Errorf("error during rate limit validation, cause: %s", err.Error())
		}
	}

	connectorTypeSet := mapset.NewThreadUnsafeSet[ApiConnectorType]()
	for _, connector := range u.Connectors {
		if connectorTypeSet.Contains(connector.Type) {
			return fmt.Errorf("there can be only one connector of type '%s'", connector.Type)
		}
		if err := connector.validate(); err != nil {
			return err
		}
		connectorTypeSet.Add(connector.Type)
	}

	if err := u.HeadConnector.validate(); err != nil {
		return fmt.Errorf("invalid head connector - '%s'", u.HeadConnector)
	}

	if !connectorTypeSet.Contains(u.HeadConnector) {
		return fmt.Errorf("there is no '%s' connector for head", u.HeadConnector)
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return err
	}

	if err := u.Methods.validate(); err != nil {
		return err
	}

	return nil
}

func (m *MethodsConfig) validate() error {
	if m.BanDuration <= 0 {
		return errors.New("the method ban duration can't be less than 0")
	}

	enabled := mapset.NewThreadUnsafeSet[string]()

	for _, enabledMethod := range m.EnableMethods {
		enabled.Add(enabledMethod)
	}

	for _, disabledMethod := range m.DisableMethods {
		if enabled.Contains(disabledMethod) {
			return fmt.Errorf("the method '%s' must not be enabled and disabled at the same time", disabledMethod)
		}
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
		return fmt.Errorf("url must be specified for connector '%s'", c.Type)
	}

	return nil
}

func (t ApiConnectorType) validate() error {
	switch t {
	case Grpc, JsonRpc, Rest, Ws:
	default:
		return fmt.Errorf("invalid connector type - '%s'", t)
	}
	return nil
}

func (r *RateLimitBudgetConfig) validate() error {

	for i, budget := range r.Budgets {
		if budget.Name == "" {
			return fmt.Errorf("rate limit budget name cannot be empty at index %d", i)
		}
		if err := budget.validate(); err != nil {
			return fmt.Errorf("error during rate limit budget '%s' validation, cause: %s", budget.Name, err.Error())
		}
	}
	return nil
}

func (r *RateLimitBudget) validate() error {
	if r.Name == "" {
		return errors.New("rate limit budget name cannot be empty")
	}

	if err := r.validate(); err != nil {
		return fmt.Errorf("rate limit budget '%s' validation error: %s", r.Name, err.Error())
	}

	return nil
}
