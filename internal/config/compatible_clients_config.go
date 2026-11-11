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
	Enabled *bool `yaml:"enabled"`
	// AllowUndetected lets a node whose client type and version cannot be probed pass the
	// check instead of being held out until it answers. Default true.
	AllowUndetected *bool         `yaml:"allow-undetected"`
	Url             string        `yaml:"url"`
	RefreshInterval time.Duration `yaml:"refresh-interval"`
}

func (c *CompatibleClientsConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

func (c *CompatibleClientsConfig) AllowsUndetected() bool {
	return c.AllowUndetected != nil && *c.AllowUndetected
}

func (c *CompatibleClientsConfig) setDefaults(upstreamMode UpstreamMode) {
	if c.Enabled == nil {
		c.Enabled = new(upstreamMode == StrictMode)
	}
	if c.AllowUndetected == nil {
		c.AllowUndetected = new(true)
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
