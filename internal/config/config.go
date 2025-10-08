package config

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

const (
	AppName           = "nodecore"
	DefaultConfigPath = "./nodecore.yml"
	ConfigPathVar     = "NODECORE_CONFIG_PATH"
)

type AppConfig struct {
	ServerConfig   *ServerConfig   `yaml:"server"`
	UpstreamConfig *UpstreamConfig `yaml:"upstream-config"`
	CacheConfig    *CacheConfig    `yaml:"cache"`
	AuthConfig     *AuthConfig     `yaml:"auth"`
}

type AuthConfig struct {
	Enabled               bool                   `yaml:"enabled"`
	RequestStrategyConfig *RequestStrategyConfig `yaml:"request-strategy"`
	KeyConfigs            []*KeyConfig           `yaml:"key-management"`
}

type KeyConfig struct {
	Id             string          `yaml:"id"`
	Type           KeyType         `yaml:"type"`
	LocalKeyConfig *LocalKeyConfig `yaml:"local"`
}

type KeyType string

const (
	Local KeyType = "local"
)

type RequestStrategyConfig struct {
	Type                       RequestStrategyType         `yaml:"type"`
	TokenRequestStrategyConfig *TokenRequestStrategyConfig `yaml:"token"`
	JwtRequestStrategyConfig   *JwtRequestStrategyConfig   `yaml:"jwt"`
}

type RequestStrategyType string

const (
	Token RequestStrategyType = "token"
	Jwt   RequestStrategyType = "jwt"
)

type TokenRequestStrategyConfig struct {
	Value string `yaml:"value"`
}

type JwtRequestStrategyConfig struct {
	PublicKey          string `yaml:"public-key"`
	AllowedIssuer      string `yaml:"allowed-issuer"`
	ExpirationRequired bool   `yaml:"expiration-required"`
}

type AuthMethods struct {
	Allowed   []string `yaml:"allowed"`
	Forbidden []string `yaml:"forbidden"`
}

type AuthContracts struct {
	Allowed []string `yaml:"allowed"`
}

type KeySettingsConfig struct {
	AllowedIps    []string       `yaml:"allowed-ips"`
	Methods       *AuthMethods   `yaml:"methods"`
	AuthContracts *AuthContracts `yaml:"contracts"`
}

type LocalKeyConfig struct {
	Key               string             `yaml:"key"`
	KeySettingsConfig *KeySettingsConfig `yaml:"settings"`
}

type ServerConfig struct {
	Port            int              `yaml:"port"`
	MetricsPort     int              `yaml:"metrics-port"`
	PprofPort       int              `yaml:"pprof-port"`
	TlsConfig       *TlsConfig       `yaml:"tls"`
	PyroscopeConfig *PyroscopeConfig `yaml:"pyroscope-config"`
}

type PyroscopeConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Url      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

func (p *PyroscopeConfig) GetServerAddress() string {
	return p.Url
}

func (p *PyroscopeConfig) GetServerUsername() string {
	return p.Username
}

func (p *PyroscopeConfig) GetServerPassword() string {
	return p.Password
}

type TlsConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Certificate string `yaml:"certificate"`
	Key         string `yaml:"key"`
	Ca          string `yaml:"ca"`
}

type UpstreamConfig struct {
	Upstreams         []*Upstream               `yaml:"upstreams"`
	ChainDefaults     map[string]*ChainDefaults `yaml:"chain-defaults"`
	FailsafeConfig    *FailsafeConfig           `yaml:"failsafe-config"`
	ScorePolicyConfig *ScorePolicyConfig        `yaml:"score-policy-config"`
	IntegrityConfig   *IntegrityConfig          `yaml:"integrity"`
}

type IntegrityConfig struct {
	Enabled bool `yaml:"enabled"`
}

type ScorePolicyConfig struct {
	CalculationInterval         time.Duration `yaml:"calculation-interval"`
	CalculationFunctionName     string        `yaml:"calculation-function-name"`      // a func name from a 'defaultRatingFunctions' map
	CalculationFunctionFilePath string        `yaml:"calculation-function-file-path"` // a path to the file with a function

	calculationFunc goja.Callable
}

var registry = new(require.Registry)

func (s *ScorePolicyConfig) GetScoreFunc() (goja.Callable, error) {
	if s.calculationFunc == nil {
		sortUpstreams, err := s.compileFunc()
		if err != nil {
			panic(err)
		}
		s.calculationFunc = sortUpstreams
	}
	return s.calculationFunc, nil
}

func (s *ScorePolicyConfig) compileFunc() (goja.Callable, error) {
	var tsFunc string
	if s.CalculationFunctionName != "" {
		tsFunc = defaultRatingFunctions[s.CalculationFunctionName]
	} else {
		funcBytes, err := os.ReadFile(s.CalculationFunctionFilePath)
		if err != nil {
			return nil, err
		}
		tsFunc = string(funcBytes)
	}

	result := api.Transform(tsFunc, api.TransformOptions{
		Loader: api.LoaderTS,
	})
	if len(result.Errors) > 0 {
		errorsText := lo.Map(result.Errors, func(item api.Message, index int) string {
			return item.Text
		})
		return nil, errors.New(strings.Join(errorsText, "; "))
	}

	vm := goja.New()
	_, err := vm.RunString(string(result.Code))
	if err != nil {
		return nil, err
	}
	registry.Enable(vm)
	console.Enable(vm)

	valueFunc := vm.Get("sortUpstreams")
	if valueFunc == nil {
		return nil, errors.New(`no sortUpstreams() function in the specified script`)
	}
	sortUpstreams, ok := goja.AssertFunction(valueFunc)
	if !ok {
		return nil, errors.New("sortUpstreams is not a function")
	}
	return sortUpstreams, nil
}

type FailsafeConfig struct {
	HedgeConfig   *HedgeConfig   `yaml:"hedge"`
	TimeoutConfig *TimeoutConfig `yaml:"timeout"`
	RetryConfig   *RetryConfig   `yaml:"retry"`
}

type RetryConfig struct {
	Attempts int            `yaml:"attempts"`
	Delay    time.Duration  `yaml:"delay"`
	MaxDelay *time.Duration `yaml:"max-delay"`
	Jitter   *time.Duration `yaml:"jitter"`
}

type HedgeConfig struct { // works only on the execution flow level
	Delay time.Duration `yaml:"delay"`
	Count int           `yaml:"max"`
}

type TimeoutConfig struct {
	Timeout time.Duration `yaml:"duration"`
}

type ChainDefaults struct {
	PollInterval time.Duration `yaml:"poll-interval"`
}

type Upstream struct {
	Id             string                `yaml:"id"`
	ChainName      string                `yaml:"chain"`
	Connectors     []*ApiConnectorConfig `yaml:"connectors"`
	HeadConnector  ApiConnectorType      `yaml:"head-connector"`
	PollInterval   time.Duration         `yaml:"poll-interval"`
	Methods        *MethodsConfig        `yaml:"methods"`
	FailsafeConfig *FailsafeConfig       `yaml:"failsafe-config"`
}

type MethodsConfig struct {
	BanDuration    time.Duration `yaml:"ban-duration"`
	EnableMethods  []string      `yaml:"enable"`
	DisableMethods []string      `yaml:"disable"`
}

type ApiConnectorType string

const (
	JsonRpc ApiConnectorType = "json-rpc"
	Rest    ApiConnectorType = "rest"
	Grpc    ApiConnectorType = "grpc"
	Ws      ApiConnectorType = "websocket"
)

var connectorTypesRating = map[ApiConnectorType]int{
	JsonRpc: 0,
	Rest:    1,
	Grpc:    2,
	Ws:      3,
}

type ApiConnectorConfig struct {
	Type    ApiConnectorType  `yaml:"type"`
	Url     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

type CacheConfig struct {
	ReceiveTimeout  time.Duration           `yaml:"receive-timeout"`
	CacheConnectors []*CacheConnectorConfig `yaml:"connectors"`
	CachePolicies   []*CachePolicyConfig    `yaml:"policies"`
}

type CacheConnectorDriver string

const (
	Memory   CacheConnectorDriver = "memory"
	Redis    CacheConnectorDriver = "redis"
	Postgres CacheConnectorDriver = "postgres"
)

type CacheConnectorConfig struct {
	Id       string                        `yaml:"id"`
	Driver   CacheConnectorDriver          `yaml:"driver"`
	Redis    *RedisCacheConnectorConfig    `yaml:"redis"`
	Memory   *MemoryCacheConnectorConfig   `yaml:"memory"`
	Postgres *PostgresCacheConnectorConfig `yaml:"postgres"`
}

type PostgresCacheConnectorConfig struct {
	Url                   string         `yaml:"url"`
	QueryTimeout          *time.Duration `yaml:"query-timeout"`
	CacheTable            string         `yaml:"cache-table"`
	ExpiredRemoveInterval time.Duration  `yaml:"expired-remove-interval"`
}

type MemoryCacheConnectorConfig struct {
	MaxItems              int           `yaml:"max-items"`
	ExpiredRemoveInterval time.Duration `yaml:"expired-remove-interval"`
}

type RedisCacheConnectorConfig struct {
	FullUrl  string                             `yaml:"full-url"`
	Address  string                             `yaml:"address"`
	Username string                             `yaml:"username"`
	Password string                             `yaml:"password"`
	DB       *int                               `yaml:"db"`
	Timeouts *RedisCacheConnectorTimeoutsConfig `yaml:"timeouts"`
	Pool     *RedisCacheConnectorPoolConfig     `yaml:"pool"`
}

type RedisCacheConnectorTimeoutsConfig struct {
	ConnectTimeout *time.Duration `yaml:"connect-timeout"`
	ReadTimeout    *time.Duration `yaml:"read-timeout"`
	WriteTimeout   *time.Duration `yaml:"write-timeout"`
}

type RedisCacheConnectorPoolConfig struct {
	Size            int            `yaml:"size"`
	PoolTimeout     *time.Duration `yaml:"pool-timeout"`
	MinIdleConns    int            `yaml:"min-idle-conns"`
	MaxIdleConns    int            `yaml:"max-idle-conns"`
	MaxActiveConns  int            `yaml:"max-active-conns"`
	ConnMaxIdleTime *time.Duration `yaml:"conn-max-idle-time"`
	ConnMaxLifeTime *time.Duration `yaml:"conn-max-life-time"`
}

type FinalizationType string

const (
	Finalized FinalizationType = "finalized"
	None      FinalizationType = "none"
)

type CachePolicyConfig struct {
	Id               string           `yaml:"id"`
	Chain            string           `yaml:"chain"`
	Method           string           `yaml:"method"`
	FinalizationType FinalizationType `yaml:"finalization-type"`
	CacheEmpty       bool             `yaml:"cache-empty"`
	Connector        string           `yaml:"connector-id"`
	ObjectMaxSize    string           `yaml:"object-max-size"`
	TTL              string           `yaml:"ttl"`
}

func NewAppConfig() (*AppConfig, error) {
	configPath := os.Getenv(ConfigPathVar)
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	log.Debug().Msgf("reading the config file %s", configPath)

	file, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	appConfig := AppConfig{}
	err = yaml.Unmarshal(file, &appConfig)
	if err != nil {
		return nil, err
	}

	appConfig.setDefaults()
	err = appConfig.validate()
	if err != nil {
		return nil, err
	}

	return &appConfig, nil
}
