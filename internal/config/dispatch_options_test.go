package config

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGetDispatchOptionsUsesModeFallbacks(t *testing.T) {
	defaultCfg := &UpstreamConfig{Mode: DefaultMode}
	strictCfg := &UpstreamConfig{Mode: StrictMode}

	defaultOptions := defaultCfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.False(t, *defaultOptions.Broadcast)
	assert.False(t, *defaultOptions.MaximumValue)
	assert.False(t, *defaultOptions.NotNull)

	strictOptions := strictCfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.True(t, *strictOptions.Broadcast)
	assert.True(t, *strictOptions.MaximumValue)
	assert.True(t, *strictOptions.NotNull)
}

func TestGetDispatchOptionsUsesChainDefaults(t *testing.T) {
	cfg := &UpstreamConfig{
		Mode: DefaultMode,
		ChainDefaults: map[string]*ChainDefaults{
			chains.ETHEREUM.String(): {Dispatch: &DispatchOptions{Broadcast: lo.ToPtr(true), MaximumValue: lo.ToPtr(true), NotNull: lo.ToPtr(true)}},
		},
	}

	ethOptions := cfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.True(t, *ethOptions.Broadcast)
	assert.True(t, *ethOptions.MaximumValue)
	assert.True(t, *ethOptions.NotNull)

	bscOptions := cfg.GetDispatchOptions(chains.BSC.String())
	assert.False(t, *bscOptions.Broadcast)
	assert.False(t, *bscOptions.MaximumValue)
	assert.False(t, *bscOptions.NotNull)
}

func TestGetDispatchOptionsChainDefaultsOverrideStrictFallback(t *testing.T) {
	cfg := &UpstreamConfig{
		Mode: StrictMode,
		ChainDefaults: map[string]*ChainDefaults{
			chains.ETHEREUM.String(): {Dispatch: &DispatchOptions{Broadcast: lo.ToPtr(false), MaximumValue: lo.ToPtr(false), NotNull: lo.ToPtr(false)}},
		},
	}

	options := cfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.False(t, *options.Broadcast)
	assert.False(t, *options.MaximumValue)
	assert.False(t, *options.NotNull)
}

func TestDispatchOptionsParseFromChainDefaults(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
mode: default
chain-defaults:
  ethereum:
    dispatch:
      broadcast: true
      maximum-value: true
      not-null: true
`), &cfg)

	require.NoError(t, err)
	options := cfg.GetDispatchOptions(chains.ETHEREUM.String())
	assert.True(t, *options.Broadcast)
	assert.True(t, *options.MaximumValue)
	assert.True(t, *options.NotNull)
}
