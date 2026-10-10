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
	appConfig, err := LoadAppConfig(ConfigPath())
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

// ConfigPath is the config file nodecore was pointed at: NODECORE_CONFIG_PATH, or
// the default path when the variable is unset.
func ConfigPath() string {
	configPath := os.Getenv(ConfigPathVar)
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	return configPath
}

func LoadAppConfig(configPath string) (*AppConfig, error) {
	log.Debug().Msgf("reading the config file %s", configPath)

	file, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	return ParseAppConfig(file)
}

// ParseAppConfig turns the bytes of a config file into a validated config with
// all the defaults applied. It is what both the startup and a reload go through,
// so a reload accepts exactly the files a restart would.
func ParseAppConfig(file []byte) (*AppConfig, error) {
	appConfig := AppConfig{}
	err := yaml.Unmarshal(file, &appConfig)
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
