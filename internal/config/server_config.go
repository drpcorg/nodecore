package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type ServerConfig struct {
	Port            int              `yaml:"port"`
	GrpcPort        int              `yaml:"grpc-port"`
	MetricsPort     int              `yaml:"metrics-port"`
	PprofPort       int              `yaml:"pprof-port"`
	HealthPort      int              `yaml:"health-port"`
	TlsConfig       *TlsConfig       `yaml:"tls"`
	PyroscopeConfig *PyroscopeConfig `yaml:"pyroscope-config"`
	GrpcAuthConfig  *GrpcAuthConfig  `yaml:"grpc-auth"`
	TorUrl          string           `yaml:"tor-url"`
	// TrustedProxies lists CIDRs (or bare IPs) of reverse proxies in front of
	// nodecore. X-Forwarded-For is only honored when the direct peer matches one
	// of these; otherwise the direct peer is used as the client IP. Empty (the
	// default) means X-Forwarded-For is never trusted.
	TrustedProxies []string `yaml:"trusted-proxies"`
}

type GrpcAuthConfig struct {
	Enabled                bool          `yaml:"enabled"`
	PublicKeyOwner         string        `yaml:"public-key-owner"`
	ProviderPrivateKeyPath string        `yaml:"provider-private-key-path"`
	ExternalPublicKeyPath  string        `yaml:"external-public-key-path"`
	SessionTTL             time.Duration `yaml:"session-ttl"`
}

type PyroscopeConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Url            string            `yaml:"url"`
	Username       string            `yaml:"username"`
	Password       string            `yaml:"password"`
	AdditionalTags map[string]string `yaml:"additional-tags"`
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

func (p *PyroscopeConfig) GetAdditionalTags() map[string]string {
	return p.AdditionalTags
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
	if s.GrpcPort < 0 {
		return fmt.Errorf("incorrect grpc port - %d", s.GrpcPort)
	}
	if s.MetricsPort < 0 {
		return fmt.Errorf("incorrect metrics port - %d", s.MetricsPort)
	}
	if s.PprofPort < 0 {
		return fmt.Errorf("incorrect pprof port - %d", s.PprofPort)
	}
	if s.HealthPort < 0 {
		return fmt.Errorf("incorrect health port - %d", s.HealthPort)
	}

	ports := mapset.NewThreadUnsafeSet[int](s.Port)
	if ports.Contains(s.GrpcPort) && s.GrpcPort != 0 {
		return fmt.Errorf("grpc port %d is already in use", s.GrpcPort)
	}
	ports.Add(s.GrpcPort)
	if ports.Contains(s.MetricsPort) && s.MetricsPort != 0 {
		return fmt.Errorf("metrics port %d is already in use", s.MetricsPort)
	}
	ports.Add(s.MetricsPort)
	if ports.Contains(s.PprofPort) && s.PprofPort != 0 {
		return fmt.Errorf("pprof port %d is already in use", s.PprofPort)
	}
	ports.Add(s.PprofPort)
	if ports.Contains(s.HealthPort) && s.HealthPort != 0 {
		return fmt.Errorf("health port %d is already in use", s.HealthPort)
	}

	if _, err := utils.ParseTrustedProxies(s.TrustedProxies); err != nil {
		return fmt.Errorf("trusted-proxies validation error - %s", err.Error())
	}

	if err := s.TlsConfig.validate(); err != nil {
		return fmt.Errorf("tls config validation error - %s", err.Error())
	}

	if err := s.PyroscopeConfig.validate(); err != nil {
		return err
	}

	if err := s.GrpcAuthConfig.validate(); err != nil {
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

func (g *GrpcAuthConfig) validate() error {
	if !g.Enabled {
		return nil
	}
	if g.PublicKeyOwner == "" {
		return errors.New("grpc auth is enabled, public-key-owner must be specified")
	}
	if strings.TrimSpace(g.ProviderPrivateKeyPath) == "" {
		return errors.New("grpc auth is enabled, provider-private-key-path must be specified")
	}
	if strings.TrimSpace(g.ExternalPublicKeyPath) == "" {
		return errors.New("grpc auth is enabled, external-public-key-path must be specified")
	}
	if _, err := os.Stat(g.ProviderPrivateKeyPath); err != nil {
		return fmt.Errorf("grpc auth provider-private-key-path is invalid: %w", err)
	}
	if _, err := os.Stat(g.ExternalPublicKeyPath); err != nil {
		return fmt.Errorf("grpc auth external-public-key-path is invalid: %w", err)
	}
	return nil
}
