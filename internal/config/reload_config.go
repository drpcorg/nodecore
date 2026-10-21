package config

import (
	"errors"
	"time"
)

// ConfigReloadConfig is the top-level config-reload section. A reload re-reads the config
// file and applies its upstream list; it always runs on SIGHUP, and additionally whenever
// the file's content changes when WatchInterval is set.
type ConfigReloadConfig struct {
	// WatchInterval is how often the file is checked for changes. Zero means signal-only.
	WatchInterval time.Duration `yaml:"watch-interval"`
}

func (c *ConfigReloadConfig) validate() error {
	if c.WatchInterval < 0 {
		return errors.New("watch-interval can't be less than 0")
	}
	return nil
}
