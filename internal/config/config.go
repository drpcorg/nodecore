package config

import (
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"time"
)

const (
	AppName           = "dShaltie"
	DefaultConfigPath = "./dShaltie.yml"
)

type AppConfig struct {
	ServerConfig   *ServerConfig   `yaml:"server"`
	UpstreamConfig *UpstreamConfig `yaml:"upstream-config"`
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
	Id             string             `yaml:"id"`
	ChainName      string             `yaml:"chain"`
	Connectors     []*ConnectorConfig `yaml:"connectors"`
	HeadConnector  ConnectorType      `yaml:"head-connector"`
	PollInterval   time.Duration      `yaml:"poll-interval"`
	Methods        *MethodsConfig     `yaml:"methods"`
	FailsafeConfig *FailsafeConfig    `yaml:"failsafe-config"`
}

type MethodsConfig struct {
	EnableMethods  []string `yaml:"enable"`
	DisableMethods []string `yaml:"disable"`
}

type ConnectorType string

const (
	JsonRpc ConnectorType = "json-rpc"
	Rest    ConnectorType = "rest"
	Grpc    ConnectorType = "grpc"
	Ws      ConnectorType = "websocket"
)

var connectorTypesRating = map[ConnectorType]int{
	JsonRpc: 0,
	Rest:    1,
	Grpc:    2,
	Ws:      3,
}

type ConnectorConfig struct {
	Type    ConnectorType     `yaml:"type"`
	Url     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

func NewAppConfig() (*AppConfig, error) {
	configPath := os.Getenv("DSHALTIE_CONFIG_PATH")
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
