package config

import (
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

const (
	defaultPort     = 9090
	defaultInterval = 1 * time.Minute
)

func (a *AppConfig) setDefaults() {
	if a.UpstreamConfig == nil {
		a.UpstreamConfig = &UpstreamConfig{}
	}
	if a.ServerConfig == nil {
		a.ServerConfig = &ServerConfig{}
	}
	a.ServerConfig.setDefaults()
	if a.CacheConfig == nil {
		a.CacheConfig = &CacheConfig{}
	}
	if a.CacheConfig != nil {
		a.CacheConfig.setDefaults()
	}
	if a.AuthConfig != nil {
		a.AuthConfig.setDefaults()
	}
	a.UpstreamConfig.setDefaults()
}

func (a *AuthConfig) setDefaults() {
	if len(a.KeyConfigs) > 0 {
		for _, key := range a.KeyConfigs {
			key.setDefaults()
		}
	}
}

func (k *KeyConfig) setDefaults() {
	if k.LocalKeyConfig != nil {
		k.LocalKeyConfig.setDefaults()
	}
}

func (l *LocalKeyConfig) setDefaults() {
	if l.KeySettingsConfig != nil {
		l.KeySettingsConfig.setDefaults()
	}
}

func (a *KeySettingsConfig) setDefaults() {
	if a.Methods == nil {
		a.Methods = &AuthMethods{}
	}
	if a.AuthContracts == nil {
		a.AuthContracts = &AuthContracts{}
	}
}

func (s *ServerConfig) setDefaults() {
	if s.Port == 0 {
		s.Port = defaultPort
	}
	if s.PyroscopeConfig == nil {
		s.PyroscopeConfig = &PyroscopeConfig{}
	}
	if s.TlsConfig == nil {
		s.TlsConfig = &TlsConfig{}
	}
}

func (c *CacheConfig) setDefaults() {
	if c.ReceiveTimeout == 0 {
		c.ReceiveTimeout = 1 * time.Second
	}
	for _, connector := range c.CacheConnectors {
		connector.setDefaults()
	}
	for _, policy := range c.CachePolicies {
		policy.setDefaults()
	}
}

func (p *CachePolicyConfig) setDefaults() {
	if p.ObjectMaxSize == "" {
		p.ObjectMaxSize = "500KB"
	}
	if p.FinalizationType == "" {
		p.FinalizationType = None
	}
	if p.TTL == "" {
		p.TTL = "10m"
	}
	if p.TTL == "0" {
		p.TTL = "0s"
	}
}

func (c *CacheConnectorConfig) setDefaults() {
	switch c.Driver {
	case Memory:
		if c.Memory == nil {
			c.Memory = &MemoryCacheConnectorConfig{}
		}
		if c.Memory.MaxItems == 0 {
			c.Memory.MaxItems = 10000
		}
		if c.Memory.ExpiredRemoveInterval == 0 {
			c.Memory.ExpiredRemoveInterval = 30 * time.Second
		}
	case Redis:
		if c.Redis == nil {
			c.Redis = &RedisCacheConnectorConfig{}
		}
		c.Redis.setDefaults()
	case Postgres:
		if c.Postgres == nil {
			c.Postgres = &PostgresCacheConnectorConfig{}
		}
		c.Postgres.setDefaults()
	}
}

func (p *PostgresCacheConnectorConfig) setDefaults() {
	if p.ExpiredRemoveInterval == 0 {
		p.ExpiredRemoveInterval = 30 * time.Second
	}
	if p.QueryTimeout == nil {
		p.QueryTimeout = lo.ToPtr(300 * time.Millisecond)
	}
	if p.CacheTable == "" {
		p.CacheTable = "cache_rpc"
	}
}

func (r *RedisCacheConnectorConfig) setDefaults() {
	if r.DB == nil {
		r.DB = lo.ToPtr(0)
	}
	if r.Timeouts == nil {
		r.Timeouts = &RedisCacheConnectorTimeoutsConfig{}
	}
	r.Timeouts.setDefaults()
	if r.Pool == nil {
		r.Pool = &RedisCacheConnectorPoolConfig{}
	}
	r.Pool.setDefaults(r.Timeouts)
}

func (p *RedisCacheConnectorPoolConfig) setDefaults(timeouts *RedisCacheConnectorTimeoutsConfig) {
	if p.Size == 0 {
		p.Size = 10 * runtime.GOMAXPROCS(0)
	}
	if p.PoolTimeout == nil {
		p.PoolTimeout = lo.ToPtr((*timeouts.ReadTimeout) + (1 * time.Second))
	}
	if p.ConnMaxIdleTime == nil {
		p.ConnMaxIdleTime = lo.ToPtr(30 * time.Minute)
	}
	if p.ConnMaxLifeTime == nil {
		p.ConnMaxLifeTime = lo.ToPtr(time.Duration(0))
	}
}

func (r *RedisCacheConnectorTimeoutsConfig) setDefaults() {
	if r.ConnectTimeout == nil {
		r.ConnectTimeout = lo.ToPtr(500 * time.Millisecond)
	}
	if r.ReadTimeout == nil {
		r.ReadTimeout = lo.ToPtr(200 * time.Millisecond)
	}
	if r.WriteTimeout == nil {
		r.WriteTimeout = lo.ToPtr(200 * time.Millisecond)
	}
}

func (u *UpstreamConfig) setDefaults() {
	if u.FailsafeConfig == nil {
		u.FailsafeConfig = &FailsafeConfig{}
	}
	if u.FailsafeConfig.RetryConfig != nil {
		u.FailsafeConfig.RetryConfig.setDefaults()
	}
	if u.FailsafeConfig.HedgeConfig != nil {
		u.FailsafeConfig.HedgeConfig.setDefaults()
	}
	if u.ScorePolicyConfig == nil {
		u.ScorePolicyConfig = &ScorePolicyConfig{}
	}
	u.ScorePolicyConfig.setDefaults()
	for _, upstream := range u.Upstreams {
		chainDefaults := u.ChainDefaults[upstream.ChainName]
		upstream.setDefaults(chainDefaults)
	}
	if u.IntegrityConfig == nil {
		u.IntegrityConfig = &IntegrityConfig{}
	}
}

func (s *ScorePolicyConfig) setDefaults() {
	if s.CalculationInterval == 0 {
		s.CalculationInterval = 10 * time.Second
	}
	if s.CalculationFunctionName == "" && s.CalculationFunctionFilePath == "" {
		log.Warn().Msgf("no explicit rating function is specified, '%s' will be used to calculate rating", DefaultLatencyPolicyFuncName)
		s.CalculationFunctionName = DefaultLatencyPolicyFuncName
	}
}

func (u *Upstream) setDefaults(defaults *ChainDefaults) {
	if u.Methods == nil {
		u.Methods = &MethodsConfig{}
	}
	u.Methods.setDefaults()
	if u.FailsafeConfig == nil {
		u.FailsafeConfig = &FailsafeConfig{}
	}
	if u.FailsafeConfig != nil {
		if u.FailsafeConfig.RetryConfig != nil {
			u.FailsafeConfig.RetryConfig.setDefaults()
		}
	}
	if u.HeadConnector == "" && len(u.Connectors) > 0 {
		filteredConnectors := lo.Filter(u.Connectors, func(item *ApiConnectorConfig, index int) bool {
			_, ok := connectorTypesRating[item.Type]
			return ok
		})

		if len(filteredConnectors) > 0 {
			defaultHeadConnectorType := lo.MinBy(filteredConnectors, func(a *ApiConnectorConfig, b *ApiConnectorConfig) bool {
				return connectorTypesRating[a.Type] < connectorTypesRating[b.Type]
			}).Type
			u.HeadConnector = defaultHeadConnectorType
		}
	}
	if u.PollInterval == 0 {
		pollInterval := defaultInterval
		if defaults != nil && defaults.PollInterval != 0 {
			// set the chain default poll interval only if there is no explicit value on the upstream level
			pollInterval = defaults.PollInterval
		}
		u.PollInterval = pollInterval
	}
}

func (m *MethodsConfig) setDefaults() {
	if m.BanDuration == 0 {
		m.BanDuration = 5 * time.Minute
	}
}

func (r *RetryConfig) setDefaults() {
	if r.Attempts == 0 {
		r.Attempts = 3
	}
	if r.Delay == 0 {
		r.Delay = 300 * time.Millisecond
	}
}

func (h *HedgeConfig) setDefaults() {
	if h.Delay == 0 {
		h.Delay = 1 * time.Second
	}
	if h.Count == 0 {
		h.Count = 2
	}
}
