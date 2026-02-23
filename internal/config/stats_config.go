package config

import (
	"errors"
	"fmt"
	"time"
)

type StatsConfig struct {
	Enabled       bool            `yaml:"enabled"`
	Type          IntegrationType `yaml:"type"`
	FlushInterval time.Duration   `yaml:"flush-interval"`
}

func (s *StatsConfig) validate() error {
	if err := s.Type.validate(); err != nil {
		return fmt.Errorf("stats type validation error - %s", err.Error())
	}
	if s.FlushInterval.Minutes() < 3 {
		return errors.New("stats flush-interval must be greater than 3 minutes")
	}
	return nil
}
