package main

import (
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const noEquivalentMessage = "no nodecore equivalent"

// convertOptions converts a dshackle `options` mapping. Only options present in the input are
// set; everything else is left to nodecore's mode defaults. fieldPrefix is put before the
// option name in report fields. It returns nil when opts does not exist or nothing was
// carried over.
//
// An option whose value has the wrong type is not handled here: the getter leaves it unread
// and the catch-all of the conversion flow reports it.
func convertOptions(opts *section, scope, fieldPrefix string, r *report) *chains.Options {
	if !opts.exists() {
		return nil
	}
	converted := new(chains.Options)

	converted.ValidateSyncing = optionalBool(opts, "validate-syncing")
	converted.ValidatePeers = optionalBool(opts, "validate-peers")
	converted.ValidateCallLimit = optionalBool(opts, "validate-call-limit")
	converted.DisableValidation = optionalBool(opts, "disable-validation")
	converted.DisableLogIndexValidation = optionalBool(opts, "disable-log-index-validation")
	converted.DisableLivenessSubscriptionValidation = optionalBool(opts, "disable-liveness-subscription-validation")
	converted.DisableSettingsValidation = optionalBool(opts, "disable-upstream-validation")
	converted.DisableLowerBoundsDetection = optionalBool(opts, "disable-bound-validation")

	// nodecore reads a zero as "not set" for both numbers, so a zero cannot be written. For
	// min-peers dshackle's zero means "do not check peers", which nodecore says with the flag.
	if peers, ok := opts.integer("min-peers"); ok {
		if peers == 0 {
			converted.ValidatePeers = new(false)
		} else {
			converted.MinPeers = peers
		}
	}
	if size, ok := opts.integer("call-limit-size"); ok {
		if size == 0 {
			r.drop(scope, fieldPrefix+"call-limit-size", "0 means 'not set' in nodecore; the default applies")
		} else {
			converted.CallLimitSize = size
		}
	}

	// dshackle switches the chain check on, nodecore switches it off
	if validateChain, ok := opts.boolean("validate-chain"); ok {
		converted.DisableChainValidation = new(!validateChain)
	}
	// dshackle counts seconds, nodecore takes a duration
	if seconds, ok := opts.integer("validation-interval"); ok {
		converted.ValidationInterval = time.Duration(seconds) * time.Second
	}

	dropOption(opts, "validate-gas-price", scope, fieldPrefix, r,
		"nodecore takes the gas-price rule from the chain registry; there is no per-upstream switch")
	dropOption(opts, "disable-pending-tx-validation", scope, fieldPrefix, r, noEquivalentMessage)
	dropOption(opts, "balance", scope, fieldPrefix, r, noEquivalentMessage)
	dropOption(opts, "timeout", scope, fieldPrefix, r, "dshackle does not use this option; nothing to carry over")

	if emptyOptions(converted) {
		return nil
	}
	return converted
}

// convertDefaults converts cluster.defaults into chain-defaults. Every chain name is resolved
// to nodecore's canonical one; a chain listed in several entries gets the options merged in
// input order, a later value winning. It returns nil when nothing was carried over.
func convertDefaults(cluster *section, r *report) map[string]*config.ChainDefaults {
	defaults := make(map[string]*config.ChainDefaults)
	for _, entry := range cluster.list("defaults") {
		names := entry.strings("chains")
		if len(names) == 0 {
			r.drop("", entry.path, "no chains listed; the options were not carried over")
			entry.ignoreAll()
			continue
		}
		options := convertOptions(entry.sub("options"), "", entry.path+".options.", r)
		for _, name := range names {
			canonical, _, ok := resolveChain(name)
			if !ok {
				r.drop("", entry.path+".chains", fmt.Sprintf("chain '%s' is not served by nodecore", name))
				continue
			}
			if options == nil {
				continue
			}
			if existing, found := defaults[canonical]; found {
				mergeOptions(existing.Options, options)
				continue
			}
			defaults[canonical] = &config.ChainDefaults{Options: cloneOptions(options)}
		}
	}
	if len(defaults) == 0 {
		return nil
	}
	return defaults
}

func optionalBool(opts *section, name string) *bool {
	if value, ok := opts.boolean(name); ok {
		return new(value)
	}
	return nil
}

func dropOption(opts *section, name, scope, fieldPrefix string, r *report, message string) {
	if opts.has(name) {
		opts.ignore(name)
		r.drop(scope, fieldPrefix+name, message)
	}
}
