package config

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

const (
	AppName           = "nodecore"
	DefaultConfigPath = "./nodecore.yml"
	ConfigPathVar     = "NODECORE_CONFIG_PATH"
)

type AppConfig struct {
	ServerConfig      *ServerConfig            `yaml:"server"`
	UpstreamConfig    *UpstreamConfig          `yaml:"upstream-config"`
	CacheConfig       *CacheConfig             `yaml:"cache"`
	AuthConfig        *AuthConfig              `yaml:"auth"`
	RateLimit         []RateLimitBudgetsConfig `yaml:"rate-limit"`
	AppStorages       []AppStorageConfig       `yaml:"app-storages"`
	IntegrationConfig *IntegrationConfig       `yaml:"integration"`
	StatsConfig       *StatsConfig             `yaml:"stats"`
	ConfigReload      *ConfigReloadConfig      `yaml:"config-reload"`

	// source is the file content this config was parsed from; see Source.
	source []byte
}

type IntegrationType string

const (
	Local IntegrationType = "local"
	Drpc  IntegrationType = "drpc"
)

func (s IntegrationType) validate() error {
	switch s {
	case Local, Drpc:
	default:
		return fmt.Errorf("invalid settings strategy type - '%s'", s)
	}
	return nil
}

func NewAppConfig() (*AppConfig, error) {
	configPath := ConfigPath()
	log.Debug().Msgf("reading the config file %s", configPath)

	file, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	appConfig, err := parseAppConfig(file)
	if err != nil {
		return nil, err
	}
	appConfig.source = file

	appConfig.setDefaults()
	err = appConfig.validate()
	if err != nil {
		return nil, err
	}

	scoreConfig := appConfig.UpstreamConfig.ScorePolicyConfig
	if scoreConfig.CalculationFunctionName != "" {
		log.Info().Msgf("the '%s' default score function will be used to calculate rating", scoreConfig.CalculationFunctionName)
	} else if scoreConfig.CalculationFunctionFilePath != "" {
		log.Info().Msgf("the score function from the %s file will be used to calculate rating", scoreConfig.CalculationFunctionFilePath)
	}

	return appConfig, nil
}

// ConfigPath is the config file nodecore reads: NODECORE_CONFIG_PATH, or ./nodecore.yml.
func ConfigPath() string {
	if configPath := os.Getenv(ConfigPathVar); configPath != "" {
		return configPath
	}
	return DefaultConfigPath
}

// Source is the file content this config was parsed from. A reload uses it as the baseline
// for everything a reload cannot change.
func (a *AppConfig) Source() []byte {
	return a.source
}

// parseAppConfig unmarshals the file and nothing more: no defaults, no validation.
func parseAppConfig(file []byte) (*AppConfig, error) {
	appConfig := AppConfig{}
	if err := yaml.Unmarshal(file, &appConfig); err != nil {
		return nil, err
	}
	return &appConfig, nil
}
