package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// LoadReloadable builds the config a reload may apply: the startup file with only the
// reloadable parts taken from file. Everything else keeps its startup value, so the
// upstreams are defaulted under the running mode and grpc-auth and validated against the
// running budgets, tor url and auth keys. The result is built from bytes every time and
// shares nothing with the live config.
func LoadReloadable(startup, file []byte) (*AppConfig, error) {
	merged, err := parseAppConfig(startup)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the startup config: %w", err)
	}
	fresh, err := parseAppConfig(file)
	if err != nil {
		return nil, err
	}
	takeReloadable(merged, fresh)
	merged.setDefaults()
	if err := merged.validate(); err != nil {
		return nil, err
	}
	return merged, nil
}

// RestartOnlyChanges names the sections in which file differs from startup apart from what
// a reload applies. Nothing is enumerated: every field of AppConfig that takeReloadable
// leaves alone is restart-only by definition, so a section added later is covered without
// touching this code. Top-level sections are named by their yaml key; upstream-config is
// descended one level so a mode change reads "upstream-config.mode"; the per-request fields
// of chain-defaults, which a reload carries but the execution flow does not re-read, are
// named "chain-defaults.<chain>.<field>".
func RestartOnlyChanges(startup, file []byte) ([]string, error) {
	base, err := parseAppConfig(startup)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the startup config: %w", err)
	}
	fresh, err := parseAppConfig(file)
	if err != nil {
		return nil, err
	}
	// both fully defaulted on their own, so a default filled in on one side only is not a difference
	base.setDefaults()
	fresh.setDefaults()

	changed := restartOnlyChainDefaults(base.UpstreamConfig.ChainDefaults, fresh.UpstreamConfig.ChainDefaults)
	takeReloadable(base, fresh) // cancels out what a reload applies, chain-defaults included
	changed = append(changed, differingFields("", reflect.ValueOf(base).Elem(), reflect.ValueOf(fresh).Elem())...)
	slices.Sort(changed)
	return changed, nil
}

// takeReloadable is the one place that says what a reload can change.
func takeReloadable(dst, src *AppConfig) {
	if dst.UpstreamConfig == nil {
		dst.UpstreamConfig = &UpstreamConfig{}
	}
	if src.UpstreamConfig == nil {
		src.UpstreamConfig = &UpstreamConfig{}
	}
	dst.UpstreamConfig.Upstreams = src.UpstreamConfig.Upstreams
	dst.UpstreamConfig.ChainDefaults = src.UpstreamConfig.ChainDefaults
}

// differingFields compares two structs of the same type field by field and names each
// exported field that differs by its yaml key, descending into *UpstreamConfig.
func differingFields(prefix string, a, b reflect.Value) []string {
	changed := make([]string, 0)
	for i := range a.NumField() {
		field := a.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		name := prefix + strings.Split(field.Tag.Get("yaml"), ",")[0]
		av, bv := a.Field(i), b.Field(i)
		switch field.Type {
		case reflect.TypeFor[*UpstreamConfig]():
			changed = append(changed, differingFields(name+".", av.Elem(), bv.Elem())...)
		case reflect.TypeFor[*ScorePolicyConfig]():
			// carries the compiled score function, which DeepEqual must not look at
			if !av.Interface().(*ScorePolicyConfig).sameSettings(bv.Interface().(*ScorePolicyConfig)) {
				changed = append(changed, name)
			}
		default:
			if !reflect.DeepEqual(av.Interface(), bv.Interface()) {
				changed = append(changed, name)
			}
		}
	}
	return changed
}

// restartOnlyChainDefaults is the one list in this file: the fields of a chain's defaults
// that a reload carries into the merged config but that nothing re-reads at runtime. It
// goes away once those settings are applied live.
func restartOnlyChainDefaults(base, fresh map[string]*ChainDefaults) []string {
	chainNames := slices.Sorted(maps.Keys(base))
	for _, name := range slices.Sorted(maps.Keys(fresh)) {
		if _, ok := base[name]; !ok {
			chainNames = append(chainNames, name)
		}
	}

	changed := make([]string, 0)
	for _, chainName := range chainNames {
		a, b := base[chainName], fresh[chainName]
		if a == nil {
			a = &ChainDefaults{}
		}
		if b == nil {
			b = &ChainDefaults{}
		}
		fields := []struct {
			name string
			x, y any
		}{
			{"label-balancing", a.LabelBalancing, b.LabelBalancing},
			{"balancing-strategy", a.BalancingStrategy, b.BalancingStrategy},
			{"dispatch", a.Dispatch, b.Dispatch},
			{"local-subscriptions", a.LocalSubscriptions, b.LocalSubscriptions},
			{"validate-lag", a.ValidateLag, b.ValidateLag},
		}
		for _, f := range fields {
			if !reflect.DeepEqual(f.x, f.y) {
				changed = append(changed, fmt.Sprintf("chain-defaults.%s.%s", chainName, f.name))
			}
		}
	}
	return changed
}
