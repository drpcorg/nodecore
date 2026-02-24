package config

import (
	"errors"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
)

type ServerConfig struct {
	Port            int              `yaml:"port"`
	MetricsPort     int              `yaml:"metrics-port"`
	PprofPort       int              `yaml:"pprof-port"`
	TlsConfig       *TlsConfig       `yaml:"tls"`
	PyroscopeConfig *PyroscopeConfig `yaml:"pyroscope-config"`
	TorUrl          string           `yaml:"tor-url"`
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

func (s *ServerConfig) validate() error {
	if s.Port < 0 {
		return fmt.Errorf("incorrect server port - %d", s.Port)
	}
	if s.MetricsPort < 0 {
		return fmt.Errorf("incorrect metrics port - %d", s.MetricsPort)
	}
	if s.PprofPort < 0 {
		return fmt.Errorf("incorrect pprof port - %d", s.PprofPort)
	}

	ports := mapset.NewThreadUnsafeSet[int](s.Port)
	if ports.Contains(s.MetricsPort) && s.MetricsPort != 0 {
		return fmt.Errorf("metrics port %d is already in use", s.MetricsPort)
	}
	ports.Add(s.MetricsPort)
	if ports.Contains(s.PprofPort) && s.PprofPort != 0 {
		return fmt.Errorf("pprof port %d is already in use", s.PprofPort)
	}

	if err := s.TlsConfig.validate(); err != nil {
		return fmt.Errorf("tls config validation error - %s", err.Error())
	}

	if err := s.PyroscopeConfig.validate(); err != nil {
		return err
	}

	return nil
}

func (t *TlsConfig) validate() error {
	if t.Enabled {
		if t.Certificate == "" {
			return errors.New("the tls certificate can't be empty")
		}
		if t.Key == "" {
			return errors.New("the tls certificate key can't be empty")
		}
	}
	return nil
}

func (p *PyroscopeConfig) validate() error {
	if p.Enabled {
		if p.Url == "" {
			return errors.New("pyroscope is enabled, url must be specified")
		}
		if p.Username == "" {
			return errors.New("pyroscope is enabled, username must be specified")
		}
		if p.Password == "" {
			return errors.New("pyroscope is enabled, password must be specified")
		}
	}

	return nil
}
