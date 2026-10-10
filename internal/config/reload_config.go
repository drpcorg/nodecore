package config

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
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

// RestartOnlyChanges names the settings that differ between the running config
// and a newly loaded one and that a reload does not apply; they keep their
// startup value until the process is restarted.
//
// What a reload does apply is left out: the upstream list, and with it the
// per-upstream defaults that chain-defaults (poll-interval, options) and mode
// feed into each upstream. Of chain-defaults only the per-chain routing
// settings are compared, and mode is reported for what it still decides at
// request time.
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
	if current.Mode != other.Mode {
		// the upstream defaults of the new mode are applied with the upstreams
		changed = append(changed, "upstream-config.mode (the dispatch and validate-lag defaults of the chains)")
	}
	for _, chain := range slices.Sorted(maps.Keys(mergeKeys(current.ChainDefaults, other.ChainDefaults))) {
		currentRouting, otherRouting := current.ChainDefaults[chain].routing(), other.ChainDefaults[chain].routing()
		for i, setting := range chainRoutingSettings {
			check(fmt.Sprintf("upstream-config.chain-defaults.%s.%s", chain, setting), currentRouting[i], otherRouting[i])
		}
	}
	check("upstream-config.failsafe-config", current.FailsafeConfig, other.FailsafeConfig)
	check("upstream-config.score-policy-config", current.ScorePolicyConfig, other.ScorePolicyConfig)
	check("upstream-config.integrity", current.IntegrityConfig, other.IntegrityConfig)
	check("upstream-config.label-balancing", current.LabelBalancing, other.LabelBalancing)
	check("upstream-config.balancing-strategy", current.BalancingStrategy, other.BalancingStrategy)
	check("upstream-config.reload", current.Reload, other.Reload)

	return changed
}

// chainRoutingSettings are the chain-defaults settings that are read while
// requests are served, in the order routing returns them. The rest of
// chain-defaults (poll-interval, options) only shapes upstreams.
var chainRoutingSettings = []string{"dispatch", "label-balancing", "balancing-strategy", "local-subscriptions", "validate-lag"}

func (c *ChainDefaults) routing() []any {
	if c == nil {
		c = &ChainDefaults{}
	}
	return []any{c.Dispatch, c.LabelBalancing, c.BalancingStrategy, c.LocalSubscriptions, c.ValidateLag}
}

func mergeKeys(first, second map[string]*ChainDefaults) map[string]struct{} {
	keys := make(map[string]struct{}, len(first)+len(second))
	for key := range first {
		keys[key] = struct{}{}
	}
	for key := range second {
		keys[key] = struct{}{}
	}
	return keys
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
