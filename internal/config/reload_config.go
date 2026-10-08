package config

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

const minReloadWatchInterval = 100 * time.Millisecond

// ReloadConfig controls how a running nodecore picks up changes of the upstream
// list. A SIGHUP always triggers a reload; the file watch is opt-in.
type ReloadConfig struct {
	// WatchInterval is how often the config file is checked for changes.
	// 0 (the default) disables the watch.
	WatchInterval time.Duration `yaml:"watch-interval"`
}

func (r *ReloadConfig) validate() error {
	if r.WatchInterval != 0 && r.WatchInterval < minReloadWatchInterval {
		return fmt.Errorf("the watch interval can't be less than %s", minReloadWatchInterval)
	}
	return nil
}

// RestartOnlyChanges names the config sections that differ between the running
// config and a newly loaded one and that a reload does not apply. Only
// upstream-config.upstreams is reloaded; everything listed here keeps its
// startup value until the process is restarted.
func (a *AppConfig) RestartOnlyChanges(loaded *AppConfig) []string {
	changed := make([]string, 0)
	check := func(name string, current, other any) {
		if !sameSettings(current, other) {
			changed = append(changed, name)
		}
	}

	check("server", a.ServerConfig, loaded.ServerConfig)
	check("cache", a.CacheConfig, loaded.CacheConfig)
	check("auth", a.AuthConfig, loaded.AuthConfig)
	check("rate-limit", a.RateLimit, loaded.RateLimit)
	check("app-storages", a.AppStorages, loaded.AppStorages)
	check("integration", a.IntegrationConfig, loaded.IntegrationConfig)
	check("stats", a.StatsConfig, loaded.StatsConfig)

	current, other := a.UpstreamConfig, loaded.UpstreamConfig
	check("upstream-config.mode", current.Mode, other.Mode)
	check("upstream-config.chain-defaults", current.ChainDefaults, other.ChainDefaults)
	check("upstream-config.failsafe-config", current.FailsafeConfig, other.FailsafeConfig)
	check("upstream-config.score-policy-config", current.ScorePolicyConfig, other.ScorePolicyConfig)
	check("upstream-config.integrity", current.IntegrityConfig, other.IntegrityConfig)
	check("upstream-config.label-balancing", current.LabelBalancing, other.LabelBalancing)
	check("upstream-config.balancing-strategy", current.BalancingStrategy, other.BalancingStrategy)
	check("upstream-config.reload", current.Reload, other.Reload)

	return changed
}

// sameSettings compares two config sections by what a config file can say about
// them. The running config also carries state built while the process works (a
// compiled score function, parsed prefixes); comparing the structs themselves
// would report that as a change.
func sameSettings(current, other any) bool {
	currentYaml, err := yaml.Marshal(current)
	if err != nil {
		return false
	}
	otherYaml, err := yaml.Marshal(other)
	if err != nil {
		return false
	}
	return bytes.Equal(currentYaml, otherYaml)
}
