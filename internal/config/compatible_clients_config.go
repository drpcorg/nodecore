package config

import (
	"errors"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
)

// CompatibleClientsConfig is the upstream-config.compatible-clients section: whether
// node clients are checked against the compatible-clients rules, where the rules are
// refreshed from and how often.
type CompatibleClientsConfig struct {
	Enabled         *bool         `yaml:"enabled"`
	Url             string        `yaml:"url"`
	RefreshInterval time.Duration `yaml:"refresh-interval"`
}

func (c *CompatibleClientsConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

func (c *CompatibleClientsConfig) setDefaults(upstreamMode UpstreamMode) {
	if c.Enabled == nil {
		c.Enabled = new(upstreamMode == StrictMode)
	}
	if c.Url == "" {
		c.Url = compatible_clients.DefaultUrl
	}
	if c.RefreshInterval == 0 {
		c.RefreshInterval = 10 * time.Minute
	}
}

func (c *CompatibleClientsConfig) validate() error {
	if c.RefreshInterval <= 0 {
		return errors.New("compatible-clients refresh-interval must be greater than 0")
	}
	return nil
}
