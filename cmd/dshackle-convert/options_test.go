package main

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"time"
)

func TestConvertOptionsMapping(t *testing.T) {
	r := new(report)
	opts := mustSection(t, `
validate-syncing: false
validate-peers: true
min-peers: 10
validate-call-limit: false
call-limit-size: 2000000
disable-validation: false
disable-log-index-validation: true
disable-liveness-subscription-validation: true
validation-interval: 45
validate-chain: false
disable-upstream-validation: true
disable-bound-validation: true
`)
	out := convertOptions(opts, "eth-1", "options.", r)
	require.NotNil(t, out)
	assert.Equal(t, &chains.Options{
		ValidationInterval: 45 * time.Second, DisableValidation: new(false),
		DisableSettingsValidation: new(true), DisableChainValidation: new(true),
		DisableLowerBoundsDetection: new(true), DisableLogIndexValidation: new(true),
		DisableLivenessSubscriptionValidation: new(true),
		ValidateSyncing:                       new(false), ValidatePeers: new(true), MinPeers: 10,
		ValidateCallLimit: new(false), CallLimitSize: 2000000,
	}, out)
	assert.Empty(t, opts.unread())
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, r))
}

func TestConvertOptionsValidateChainTrue(t *testing.T) {
	out := convertOptions(mustSection(t, "validate-chain: true\n"), "a", "options.", new(report))
	assert.Equal(t, &chains.Options{DisableChainValidation: new(false)}, out)
}

func TestConvertOptionsWarnings(t *testing.T) {
	r := new(report)
	opts := mustSection(t, "validate-gas-price: false\ndisable-pending-tx-validation: true\nbalance: true\ntimeout: 60\nmin-peers: ten\n")
	assert.Nil(t, convertOptions(opts, "eth-1", "options.", r))
	text := reportText(t, r)
	assert.Contains(t, text, "  options.validate-gas-price (eth-1): nodecore takes the gas-price rule from the chain registry; there is no per-upstream switch\n")
	assert.Contains(t, text, "  options.disable-pending-tx-validation (eth-1): no nodecore equivalent\n")
	assert.Contains(t, text, "  options.balance (eth-1): no nodecore equivalent\n")
	assert.Contains(t, text, "  options.timeout (eth-1): dshackle does not use this option; nothing to carry over\n")
	assert.Equal(t, []string{"min-peers"}, opts.unread()) // left for the catch-all
}

func TestConvertOptionsMissing(t *testing.T) {
	assert.Nil(t, convertOptions(mustSection(t, "id: a\n").sub("options"), "a", "options.", new(report)))
}

func TestConvertDefaults(t *testing.T) {
	r := new(report)
	root := mustSection(t, `
cluster:
  defaults:
    - chains: [ethereum, ripple]
      options:
        min-peers: 3
        validate-peers: true
    - chains: [ETH]
      options:
        min-peers: 10
    - options:
        min-peers: 1
`)
	out := convertDefaults(root.sub("cluster"), r)
	require.Len(t, out, 1)
	assert.Equal(t, &chains.Options{MinPeers: 10, ValidatePeers: new(true)}, out["ethereum"].Options)
	text := reportText(t, r)
	assert.Contains(t, text, "  cluster.defaults[0].chains: chain 'ripple' is not served by nodecore\n")
	assert.Contains(t, text, "  cluster.defaults[2]: no chains listed; the options were not carried over\n")
	assert.Empty(t, root.unread())
}

// nodecore reads min-peers: 0 as "not set" and falls back to 1, while in dshackle it means
// "do not check peers". The flag is the only way to say that in nodecore.
func TestConvertOptionsZeroMinPeersTurnsThePeerCheckOff(t *testing.T) {
	r := new(report)
	opts := mustSection(t, "validate-peers: true\nmin-peers: 0\n")
	out := convertOptions(opts, "a", "options.", r)
	assert.Equal(t, &chains.Options{ValidatePeers: new(false)}, out)
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, r))
	assert.Empty(t, opts.unread())
}

func TestConvertOptionsZeroCallLimitSizeIsReported(t *testing.T) {
	r := new(report)
	opts := mustSection(t, "call-limit-size: 0\n")
	assert.Nil(t, convertOptions(opts, "a", "options.", r))
	assert.Contains(t, reportText(t, r), "  options.call-limit-size (a): 0 means 'not set' in nodecore; the default applies\n")
	assert.Empty(t, opts.unread())
}

func TestOptionsMergeCloneAndEmpty(t *testing.T) {
	base := &chains.Options{MinPeers: 3, ValidatePeers: new(true)}
	later := &chains.Options{MinPeers: 10, ValidationInterval: 15 * time.Second, ValidateSyncing: new(false)}
	mergeOptions(base, later)
	assert.Equal(t, &chains.Options{MinPeers: 10, ValidatePeers: new(true), ValidationInterval: 15 * time.Second, ValidateSyncing: new(false)}, base)
	assert.NotSame(t, later.ValidateSyncing, base.ValidateSyncing)

	copied := cloneOptions(base)
	assert.Equal(t, base, copied)
	assert.NotSame(t, base.ValidatePeers, copied.ValidatePeers)

	assert.True(t, emptyOptions(new(chains.Options)))
	assert.False(t, emptyOptions(base))
}
