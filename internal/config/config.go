package config

import (
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"time"
)

const (
	AppName           = "dSheltie"
	DefaultConfigPath = "./dSheltie.yml"
	ConfigPathVar     = "DSHELTIE_CONFIG_PATH"
)

type AppConfig struct {
	ServerConfig   *ServerConfig   `yaml:"server"`
	UpstreamConfig *UpstreamConfig `yaml:"upstream-config"`
	CacheConfig    *CacheConfig    `yaml:"cache"`
}

type ServerConfig struct {
	Port      int        `yaml:"port"`
	TlsConfig *TlsConfig `yaml:"tls"`
}

type TlsConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Certificate string `yaml:"certificate"`
	Key         string `yaml:"key"`
}

type UpstreamConfig struct {
	Upstreams      []*Upstream               `yaml:"upstreams"`
	ChainDefaults  map[string]*ChainDefaults `yaml:"chain-defaults"`
	FailsafeConfig *FailsafeConfig           `yaml:"failsafe-config"`
}

type FailsafeConfig struct {
	HedgeConfig   *HedgeConfig   `yaml:"hedge"`
	TimeoutConfig *TimeoutConfig `yaml:"timeout"`
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
	EnableMethods  []string `yaml:"enable"`
	DisableMethods []string `yaml:"disable"`
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
	Headers map[string]string `yaml:"headers"`
}

type CacheConfig struct {
	CacheConnectors []*CacheConnectorConfig `yaml:"connectors"`
	CachePolicies   []*CachePolicyConfig    `yaml:"policies"`
}

type CacheConnectorDriver string

const (
	Memory CacheConnectorDriver = "memory"
	Redis  CacheConnectorDriver = "redis"
)

type CacheConnectorConfig struct {
	Id     string                      `yaml:"id"`
	Driver CacheConnectorDriver        `yaml:"driver"`
	Redis  *RedisCacheConnectorConfig  `yaml:"redis"`
	Memory *MemoryCacheConnectorConfig `yaml:"memory"`
}

type MemoryCacheConnectorConfig struct {
	MaxItems              int           `yaml:"max-items"`
	ExpiredRemoveInterval time.Duration `yaml:"expired-remove-interval"`
}

type RedisCacheConnectorConfig struct {
	Address        string        `yaml:"address"`
	Password       string        `yaml:"password"`
	DB             int           `yaml:"db"`
	PoolSize       int           `yaml:"pool-size"`
	ConnectTimeout time.Duration `yaml:"connect-timeout"`
	ReadTimeout    time.Duration `yaml:"read-timeout"`
	WriteTimeout   time.Duration `yaml:"write-timeout"`
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
