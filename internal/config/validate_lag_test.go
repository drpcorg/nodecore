package config

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestValidateLagForModeDefault(t *testing.T) {
	// nil receiver defaults to disabled.
	var nilCfg *UpstreamConfig
	assert.False(t, nilCfg.ValidateLagFor(chains.ETHEREUM.String()))

	// Default mode: a chain without the flag (or with no chain-defaults) is
	// disabled.
	defaultCfg := &UpstreamConfig{Mode: DefaultMode}
	assert.False(t, defaultCfg.ValidateLagFor(chains.ETHEREUM.String()))

	otherChainCfg := &UpstreamConfig{Mode: DefaultMode, ChainDefaults: map[string]*ChainDefaults{
		chains.BSC.String(): {ValidateLag: new(false)},
	}}
	assert.False(t, otherChainCfg.ValidateLagFor(chains.ETHEREUM.String()))

	// Strict mode: a chain without the flag is enabled.
	strictCfg := &UpstreamConfig{Mode: StrictMode}
	assert.True(t, strictCfg.ValidateLagFor(chains.ETHEREUM.String()))
}

func TestValidateLagForPerChainOverride(t *testing.T) {
	// An explicit per-chain override wins over the mode default, in both
	// directions and under either mode.
	defaultCfg := &UpstreamConfig{Mode: DefaultMode, ChainDefaults: map[string]*ChainDefaults{
		chains.ETHEREUM.String(): {ValidateLag: new(false)},
		chains.POLYGON.String():  {ValidateLag: new(true)},
	}}
	assert.False(t, defaultCfg.ValidateLagFor(chains.ETHEREUM.String()))
	assert.True(t, defaultCfg.ValidateLagFor(chains.POLYGON.String()))

	strictCfg := &UpstreamConfig{Mode: StrictMode, ChainDefaults: map[string]*ChainDefaults{
		chains.ETHEREUM.String(): {ValidateLag: new(false)},
		chains.POLYGON.String():  {ValidateLag: new(true)},
	}}
	assert.False(t, strictCfg.ValidateLagFor(chains.ETHEREUM.String()))
	assert.True(t, strictCfg.ValidateLagFor(chains.POLYGON.String()))
}
