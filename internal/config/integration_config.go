package config

import (
	"errors"
	"fmt"
	"time"
)

type IntegrationConfig struct {
	Drpc *DrpcIntegrationConfig `yaml:"drpc"`
}

type DrpcIntegrationConfig struct {
	Url            string        `yaml:"url"`
	RequestTimeout time.Duration `yaml:"request-timeout"`
}

func (i *IntegrationConfig) validate() error {
	if i.Drpc != nil {
		if err := i.Drpc.validate(); err != nil {
			return fmt.Errorf("error during drpc integration validation, cause - %s", err.Error())
		}
	}

	return nil
}

func (d *DrpcIntegrationConfig) validate() error {
	if d.Url == "" {
		return errors.New("url cannot be empty")
	}
	if d.RequestTimeout < 0 {
		return errors.New("request timeout cannot be less than 0")
	}

	return nil
}
