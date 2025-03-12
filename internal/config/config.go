package config

import (
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
)

const (
	AppName           = "dShaltie"
	DefaultConfigPath = "./dShaltie.yml"
)

type AppConfig struct {
	ServerConfig *ServerConfig    `yaml:"server"`
	Projects     []*ProjectConfig `yaml:"projects"`
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

type ProjectConfig struct {
	Id        string            `yaml:"id"`
	Upstreams []*UpstreamConfig `yaml:"upstreams"`
}

type UpstreamConfig struct {
	Id         string             `yaml:"id"`
	ChainName  string             `yaml:"chain"`
	Connectors []*ConnectorConfig `yaml:"connectors"`
}

type ConnectorType string

const (
	JsonRpc ConnectorType = "json-rpc"
	Rest    ConnectorType = "rest"
	Grpc    ConnectorType = "grpc"
	Ws      ConnectorType = "websocket"
)

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
	err = appConfig.Validate()
	if err != nil {
		return nil, err
	}

	return &appConfig, nil
}

func (a *AppConfig) setDefaults() {
	//TODO: set default values
}
