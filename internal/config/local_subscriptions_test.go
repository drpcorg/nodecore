package config

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLocalSubSettingsDefaultsEnabled(t *testing.T) {
	// nil receiver, no chain-defaults, and a chain without the block all default
	// to everything enabled - preserving the always-synthesize-locally behavior.
	var nilCfg *UpstreamConfig
	assert.Equal(t, LocalSubSettings{NewHeads: true, Logs: true, PendingTx: true}, nilCfg.LocalSubSettings(chains.ETHEREUM.String()))

	emptyCfg := &UpstreamConfig{}
	assert.Equal(t, LocalSubSettings{NewHeads: true, Logs: true, PendingTx: true}, emptyCfg.LocalSubSettings(chains.ETHEREUM.String()))

	otherChainCfg := &UpstreamConfig{ChainDefaults: map[string]*ChainDefaults{
		chains.BSC.String(): {LocalSubscriptions: &LocalSubscriptionsConfig{Enable: new(false)}},
	}}
	assert.Equal(t, LocalSubSettings{NewHeads: true, Logs: true, PendingTx: true}, otherChainCfg.LocalSubSettings(chains.ETHEREUM.String()))
}

func TestLocalSubSettingsMasterDisables(t *testing.T) {
	cfg := &UpstreamConfig{ChainDefaults: map[string]*ChainDefaults{
		chains.ETHEREUM.String(): {LocalSubscriptions: &LocalSubscriptionsConfig{Enable: new(false)}},
	}}
	assert.Equal(t, LocalSubSettings{}, cfg.LocalSubSettings(chains.ETHEREUM.String()))
}

func TestLocalSubSettingsPerTypeOverridesMaster(t *testing.T) {
	cfg := &UpstreamConfig{ChainDefaults: map[string]*ChainDefaults{
		chains.ETHEREUM.String(): {LocalSubscriptions: &LocalSubscriptionsConfig{
			Enable:     new(false),
			EnableLogs: new(true), // re-enable a single type while master is off
		}},
	}}
	assert.Equal(t, LocalSubSettings{NewHeads: false, Logs: true, PendingTx: false}, cfg.LocalSubSettings(chains.ETHEREUM.String()))
}

func TestLocalSubSettingsPerTypeDisableUnderEnabledMaster(t *testing.T) {
	cfg := &UpstreamConfig{ChainDefaults: map[string]*ChainDefaults{
		chains.ETHEREUM.String(): {LocalSubscriptions: &LocalSubscriptionsConfig{
			EnableNewPendingTransactions: new(false), // master unset (defaults true), disable just one
		}},
	}}
	assert.Equal(t, LocalSubSettings{NewHeads: true, Logs: true, PendingTx: false}, cfg.LocalSubSettings(chains.ETHEREUM.String()))
}

func TestLocalSubscriptionsParseFromChainDefaults(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
mode: default
chain-defaults:
  ethereum:
    local-subscriptions:
      enable: false
      enable-logs: true
`), &cfg)

	require.NoError(t, err)
	assert.Equal(t, LocalSubSettings{NewHeads: false, Logs: true, PendingTx: false}, cfg.LocalSubSettings(chains.ETHEREUM.String()))
}
