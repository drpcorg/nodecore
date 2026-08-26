package config

import (
	"runtime"
	"strconv"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

const (
	defaultPort     = 9090
	defaultInterval = 1 * time.Minute
)

func (a *AppConfig) setDefaults() {
	if a.AppStorages == nil {
		a.AppStorages = []AppStorageConfig{}
	}
	for _, storage := range a.AppStorages {
		storage.setDefaults()
	}
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
	if a.StatsConfig != nil {
		a.StatsConfig.setDefaults()
	}
	if a.IntegrationConfig != nil {
		if a.IntegrationConfig.Drpc != nil {
			a.IntegrationConfig.Drpc.setDefaults()
		}
	}
	a.UpstreamConfig.setDefaults(a.ServerConfig.GrpcAuthConfig)
}

func (s *StatsConfig) setDefaults() {
	if s.FlushInterval == 0 {
		s.FlushInterval = 3 * time.Minute
	}
}

func (d *DrpcIntegrationConfig) setDefaults() {
	if d.RequestTimeout == 0 {
		d.RequestTimeout = 10 * time.Second
	}
}

func (a *AuthConfig) setDefaults() {
	if len(a.KeyConfigs) > 0 {
		for _, key := range a.KeyConfigs {
			key.setDefaults()
		}
	}
}

func (a *AppStorageConfig) setDefaults() {
	if a.Redis != nil {
		a.Redis.setDefaults()
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
	if s.HealthPort == 0 {
		s.HealthPort = 9096
	}
	if s.PyroscopeConfig == nil {
		s.PyroscopeConfig = &PyroscopeConfig{}
	}
	if s.TlsConfig == nil {
		s.TlsConfig = &TlsConfig{}
	}
	if s.GrpcAuthConfig == nil {
		s.GrpcAuthConfig = &GrpcAuthConfig{}
	}
	s.GrpcAuthConfig.setDefaults()
}

func (g *GrpcAuthConfig) setDefaults() {
	if g.PublicKeyOwner == "" {
		g.PublicKeyOwner = "drpc"
	}
	if g.SessionTTL == 0 {
		g.SessionTTL = 24 * time.Hour
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
		p.QueryTimeout = new(300 * time.Millisecond)
	}
	if p.CacheTable == "" {
		p.CacheTable = "cache_rpc"
	}
}

func (r *RedisStorageConfig) setDefaults() {
	if r.DB == nil {
		r.DB = new(0)
	}
	if r.Timeouts == nil {
		r.Timeouts = &RedisStorageTimeoutsConfig{}
	}
	r.Timeouts.setDefaults()
	if r.Pool == nil {
		r.Pool = &RedisStoragePoolConfig{}
	}
	r.Pool.setDefaults(r.Timeouts)
}

func (p *RedisStoragePoolConfig) setDefaults(timeouts *RedisStorageTimeoutsConfig) {
	if p.Size == 0 {
		p.Size = 10 * runtime.GOMAXPROCS(0)
	}
	if p.PoolTimeout == nil {
		p.PoolTimeout = new((*timeouts.ReadTimeout) + (1 * time.Second))
	}
	if p.ConnMaxIdleTime == nil {
		p.ConnMaxIdleTime = new(30 * time.Minute)
	}
	if p.ConnMaxLifeTime == nil {
		p.ConnMaxLifeTime = new(time.Duration(0))
	}
}

func (r *RedisStorageTimeoutsConfig) setDefaults() {
	if r.ConnectTimeout == nil {
		r.ConnectTimeout = new(500 * time.Millisecond)
	}
	if r.ReadTimeout == nil {
		r.ReadTimeout = new(200 * time.Millisecond)
	}
	if r.WriteTimeout == nil {
		r.WriteTimeout = new(200 * time.Millisecond)
	}
}

func (u *UpstreamConfig) setDefaults(grpcAuth *GrpcAuthConfig) {
	if u.Mode == "" {
		u.Mode = DefaultMode
	}
	if u.BalancingStrategy == "" {
		u.BalancingStrategy = RatingBalancingStrategy
	}
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
	u.LabelBalancing.setDefaults()
	for _, chainDefaults := range u.ChainDefaults {
		chainDefaults.LabelBalancing.setDefaults()
	}
	for _, upstream := range u.Upstreams {
		chainDefaults := u.ChainDefaults[upstream.ChainName]
		upstream.setDefaults(chainDefaults, u.Mode)
		if !grpcAuth.Disabled() {
			upstream.setSecureSignedLabel()
		}
	}
	if u.IntegrityConfig == nil {
		u.IntegrityConfig = &IntegrityConfig{}
	}
	u.IntegrityConfig.setDefaults(u.Mode)
}

func (i *IntegrityConfig) setDefaults(upstreamMode UpstreamMode) {
	if upstreamMode == StrictMode {
		if i.Enabled {
			log.Warn().Msgf("integrity feature is disabled if %s mode is active", upstreamMode)
		}
		i.Enabled = false
	}
}

func (r *RateLimitAutoTuneConfig) setDefaults() {
	if r.Period == 0 {
		r.Period = 1 * time.Minute
	}
	if r.ErrorRateThreshold == 0 {
		r.ErrorRateThreshold = 0.1
	}
	if r.InitRateLimit == 0 {
		r.InitRateLimit = 100
	}
	if r.InitRateLimitPeriod == 0 {
		r.InitRateLimitPeriod = 1 * time.Second
	}
}

func (l *LabelBalancingConfig) setDefaults() {
	if l == nil {
		return
	}
	if l.IncludeDefault == nil {
		l.IncludeDefault = new(true)
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

func (u *Upstream) setDefaults(defaults *ChainDefaults, upstreamMode UpstreamMode) {
	if u.Methods == nil {
		u.Methods = &MethodsConfig{}
	}
	u.Methods.setDefaults()
	if u.FailsafeConfig == nil {
		u.FailsafeConfig = &FailsafeConfig{}
	}
	if u.Options == nil {
		u.Options = &chains.Options{}
	}
	configuredChain := chains.GetChain(u.ChainName)
	setOptionsDefaults(u.Options, defaults, configuredChain.Settings, upstreamMode)
	if u.FailsafeConfig != nil {
		if u.FailsafeConfig.RetryConfig != nil {
			u.FailsafeConfig.RetryConfig.setDefaults()
		}
	}
	if u.HeadConnector == "" && len(u.Connectors) > 0 {
		if headConnector := u.GetBestConnector(upstreamMode); headConnector != specs.UnknownType {
			u.HeadConnector = headConnector.String()
		}
	}
	if u.RateLimitAutoTune != nil {
		u.RateLimitAutoTune.setDefaults()
	}
	if u.PollInterval == 0 {
		pollInterval := getDefaultPollInterval(u.ChainName, upstreamMode)
		if defaults != nil && defaults.PollInterval != 0 {
			// set the chain default poll interval only if there is no explicit value on the upstream level
			pollInterval = defaults.PollInterval
		}
		u.PollInterval = pollInterval
	}
	u.translateDeprecatedArchiveOption()
}

// translateDeprecatedArchiveOption turns the deprecated options.archive flag into the
// 'archive' label, so an existing config keeps its override instead of silently losing it
// to archive auto-detection. An explicit label always wins. It runs after
// setOptionsDefaults. Only the upstream-level flag is translated: setOptionsDefaults has
// never merged ArchiveCapability from chain-defaults, and the detector it replaced read the
// upstream value only, so chain-defaults options.archive never took effect.
func (u *Upstream) translateDeprecatedArchiveOption() {
	if u.Options == nil || u.Options.ArchiveCapability == nil {
		return
	}
	log.Warn().Msgf("upstream '%s': options.archive is deprecated, use the '%s' upstream label instead", u.Id, chains.ArchiveLabel)
	if _, set := u.Labels[chains.ArchiveLabel]; set {
		return
	}
	if u.Labels == nil {
		u.Labels = UpstreamLabels{}
	}
	u.Labels[chains.ArchiveLabel] = strconv.FormatBool(*u.Options.ArchiveCapability)
}

// setSecureSignedLabel advertises that this instance signs responses, so gRPC
// clients can select signing-capable providers.
func (u *Upstream) setSecureSignedLabel() {
	if _, set := u.Labels[SecureSignedLabel]; set {
		return
	}
	if u.Labels == nil {
		u.Labels = UpstreamLabels{}
	}
	u.Labels[SecureSignedLabel] = "true"
}

func getDefaultPollInterval(chainName string, upstreamMode UpstreamMode) time.Duration {
	if upstreamMode == DefaultMode {
		return defaultInterval
	}
	chain := chains.GetChain(chainName)
	if chain == chains.UnknownChain {
		return defaultInterval
	}
	return chain.Settings.ExpectedBlockTime
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
}

func (h *HedgeConfig) setDefaults() {
	if h.Delay == 0 {
		h.Delay = 1 * time.Second
	}
	if h.Count == 0 {
		h.Count = 2
	}
}
