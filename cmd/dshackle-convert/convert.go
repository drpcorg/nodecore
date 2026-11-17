package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/drpcorg/nodecore/internal/config"
)

// conversion is what one run produced. report is always set, also next to an error, so the
// caller can show why nothing was written.
type conversion struct {
	config    *config.AppConfig
	report    *report
	converted int // entries of cluster.upstreams that produced at least one upstream
	total     int // entries of cluster.upstreams
}

// convert turns a dshackle config into a nodecore one. Every upstream is validated on its
// own and left out when nodecore would refuse it; the whole output is validated once more
// before it is handed back. Anything in the input that no rule read ends up in the report.
func convert(input []byte) (*conversion, error) {
	converted := &conversion{report: new(report)}
	root, err := parseInput(input)
	if err != nil {
		return converted, err
	}

	cluster := root.sub("cluster")
	chainDefaults, defaultPeers := convertDefaults(cluster, converted.report)
	cfg := &config.AppConfig{
		ServerConfig: convertServer(root, converted.report),
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              config.StrictMode,
			BalancingStrategy: config.BaseBalancingStrategy,
			ChainDefaults:     chainDefaults,
			Upstreams:         make([]*config.Upstream, 0),
		},
	}
	converted.config = cfg
	dropRejectedDefaults(cfg, converted.report)

	entries := cluster.list("upstreams")
	converted.total = len(entries)
	for index, entry := range entries {
		kept := false
		ownPeers := readPeerCheck(entry.sub("options"))
		for _, upstream := range convertUpstream(entry, index, converted.report) {
			// only defaults that made it into the output are below the upstream
			if _, inherited := cfg.UpstreamConfig.ChainDefaults[upstream.ChainName]; inherited && upstream.Options != nil {
				ownPeers.writeOver(defaultPeers[upstream.ChainName], upstream.Options)
			}
			if err := validateUpstream(cfg, upstream); err != nil {
				converted.report.skip(upstream.Id, "rejected by nodecore validation: "+err.Error())
				continue
			}
			cfg.UpstreamConfig.Upstreams = append(cfg.UpstreamConfig.Upstreams, upstream)
			kept = true
		}
		if kept {
			converted.converted++
		}
	}

	for _, path := range root.unread() {
		converted.report.drop("", path, "not converted (unknown key or unusable value)")
	}

	if converted.total == 0 {
		return converted, errors.New("no upstreams found in cluster.upstreams")
	}
	if converted.converted == 0 {
		return converted, errors.New("no upstream could be converted")
	}
	if err := validateConfig(cfg); err != nil {
		return converted, fmt.Errorf("the converted config was rejected by nodecore validation: %w", err)
	}
	return converted, nil
}

// dropRejectedDefaults removes the chain defaults nodecore's validation refuses and reports
// them, so that one bad default does not take every upstream down with it.
func dropRejectedDefaults(cfg *config.AppConfig, r *report) {
	for _, chainName := range slices.Sorted(maps.Keys(cfg.UpstreamConfig.ChainDefaults)) {
		if err := validateChainDefaults(cfg, chainName, cfg.UpstreamConfig.ChainDefaults[chainName]); err != nil {
			delete(cfg.UpstreamConfig.ChainDefaults, chainName)
			r.drop("", "chain-defaults."+chainName, "rejected by nodecore validation: "+err.Error())
		}
	}
	if len(cfg.UpstreamConfig.ChainDefaults) == 0 {
		cfg.UpstreamConfig.ChainDefaults = nil
	}
}
