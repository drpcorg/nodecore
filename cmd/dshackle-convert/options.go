package main

import (
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const noEquivalentMessage = "no nodecore equivalent"

// peerCheck is dshackle's pair of peer options as one layer of the config wrote them: an
// entry of cluster.defaults, or an upstream. dshackle checks peers when validate-peers is on
// (its default) and min-peers is above zero (its default is 1), and it merges the two options
// one by one, a later layer winning.
//
// nodecore resolves validate-peers and min-peers one by one as well, but reads a zero
// min-peers as "not set". dshackle's zero is therefore written as validate-peers: false, and
// that flag has to be taken back when a later layer raises min-peers again. This is why the
// two options are converted as a pair and not each on its own.
type peerCheck struct {
	validate *bool
	minPeers *int64
}

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
	converted.ValidateCallLimit = optionalBool(opts, "validate-call-limit")
	converted.DisableValidation = optionalBool(opts, "disable-validation")
	converted.DisableLogIndexValidation = optionalBool(opts, "disable-log-index-validation")
	converted.DisableLivenessSubscriptionValidation = optionalBool(opts, "disable-liveness-subscription-validation")
	converted.DisableSettingsValidation = optionalBool(opts, "disable-upstream-validation")
	converted.DisableLowerBoundsDetection = optionalBool(opts, "disable-bound-validation")

	readPeerCheck(opts).write(converted)
	// nodecore reads a zero as "not set", so a zero cannot be written
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
// input order, a later value winning. The second result is the merged peer options of every
// chain as dshackle sees them, which an upstream of that chain needs for its own pair. Both
// results are nil when nothing was carried over.
func convertDefaults(cluster *section, r *report) (map[string]*config.ChainDefaults, map[string]peerCheck) {
	defaults := make(map[string]*config.ChainDefaults)
	peers := make(map[string]peerCheck)
	for _, entry := range cluster.list("defaults") {
		names := entry.strings("chains")
		if len(names) == 0 {
			r.drop("", entry.path, "no chains listed; the options were not carried over")
			entry.ignoreAll()
			continue
		}
		options := convertOptions(entry.sub("options"), "", entry.path+".options.", r)
		entryPeers := readPeerCheck(entry.sub("options"))
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
			} else {
				defaults[canonical] = &config.ChainDefaults{Options: cloneOptions(options)}
			}
			// the pair is written from the merged dshackle values, not merged as two options
			peers[canonical] = entryPeers.over(peers[canonical])
			peers[canonical].write(defaults[canonical].Options)
		}
	}
	if len(defaults) == 0 {
		return nil, nil
	}
	return defaults, peers
}

func readPeerCheck(opts *section) peerCheck {
	var read peerCheck
	if validate, ok := opts.boolean("validate-peers"); ok {
		read.validate = new(validate)
	}
	if minPeers, ok := opts.integer("min-peers"); ok {
		read.minPeers = new(minPeers)
	}
	return read
}

// over returns the pair a layer with p's options gives on top of lower.
func (p peerCheck) over(lower peerCheck) peerCheck {
	merged := lower
	if p.validate != nil {
		merged.validate = p.validate
	}
	if p.minPeers != nil {
		merged.minPeers = p.minPeers
	}
	return merged
}

// write sets validate-peers and min-peers for a layer that has nothing below it.
func (p peerCheck) write(options *chains.Options) {
	options.ValidatePeers, options.MinPeers = nil, 0
	if p.zeroMinPeers() {
		// dshackle's "do not check peers"; nodecore can only say it with the flag
		options.ValidatePeers = new(false)
		return
	}
	if p.validate != nil {
		options.ValidatePeers = new(*p.validate)
	}
	if p.minPeers != nil {
		options.MinPeers = *p.minPeers
	}
}

// writeOver corrects validate-peers of an upstream whose chain has the defaults lower, after
// write has set the pair. An upstream that sets neither option inherits the defaults as they
// are. Otherwise the flag must say what dshackle ends up doing, where inheriting it would not:
// a positive min-peers over a zero default switches the check back on, and validate-peers:
// true over a zero default leaves it off.
func (p peerCheck) writeOver(lower peerCheck, options *chains.Options) {
	checked := p.over(lower).checksPeers()
	switch {
	case p.validate != nil:
		options.ValidatePeers = new(checked)
	case p.minPeers != nil && checked && lower.zeroMinPeers():
		options.ValidatePeers = new(true)
	}
}

func (p peerCheck) checksPeers() bool {
	return (p.validate == nil || *p.validate) && (p.minPeers == nil || *p.minPeers > 0)
}

func (p peerCheck) zeroMinPeers() bool {
	return p.minPeers != nil && *p.minPeers == 0
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
