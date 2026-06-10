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

	assert.False(t, *defaultCfg.GetDispatchOptions(chains.ETHEREUM.String()).NotNull)
	assert.True(t, *strictCfg.GetDispatchOptions(chains.ETHEREUM.String()).NotNull)
}

func TestGetDispatchOptionsUsesChainDefaults(t *testing.T) {
	cfg := &UpstreamConfig{
		Mode: DefaultMode,
		ChainDefaults: map[string]*ChainDefaults{
			chains.ETHEREUM.String(): {Dispatch: &DispatchOptions{NotNull: lo.ToPtr(true)}},
		},
	}

	assert.True(t, *cfg.GetDispatchOptions(chains.ETHEREUM.String()).NotNull)
	assert.False(t, *cfg.GetDispatchOptions(chains.BSC.String()).NotNull)
}

func TestGetDispatchOptionsChainDefaultsOverrideStrictFallback(t *testing.T) {
	cfg := &UpstreamConfig{
		Mode: StrictMode,
		ChainDefaults: map[string]*ChainDefaults{
			chains.ETHEREUM.String(): {Dispatch: &DispatchOptions{NotNull: lo.ToPtr(false)}},
		},
	}

	assert.False(t, *cfg.GetDispatchOptions(chains.ETHEREUM.String()).NotNull)
}

func TestDispatchOptionsParseFromChainDefaults(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
mode: default
chain-defaults:
  ethereum:
    dispatch:
      not-null: true
`), &cfg)

	require.NoError(t, err)
	assert.True(t, *cfg.GetDispatchOptions(chains.ETHEREUM.String()).NotNull)
}
