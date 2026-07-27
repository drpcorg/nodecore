package config

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestBalancingStrategyDefaultApplied(t *testing.T) {
	// setDefaults resolves an unset global to rating.
	cfg := &UpstreamConfig{}
	cfg.setDefaults()
	assert.Equal(t, RatingBalancingStrategy, cfg.BalancingStrategyFor(chains.ETHEREUM.String()))
}

func TestBalancingStrategyForGlobalDefault(t *testing.T) {
	// The global field applies to all chains when no per-chain override exists.
	globalCfg := &UpstreamConfig{BalancingStrategy: BaseBalancingStrategy}
	assert.Equal(t, BaseBalancingStrategy, globalCfg.BalancingStrategyFor(chains.ETHEREUM.String()))
	assert.Equal(t, BaseBalancingStrategy, globalCfg.BalancingStrategyFor(chains.POLYGON.String()))

	// An override for another chain does not affect the queried chain.
	otherChainCfg := &UpstreamConfig{
		BalancingStrategy: RatingBalancingStrategy,
		ChainDefaults: map[string]*ChainDefaults{
			chains.BSC.String(): {BalancingStrategy: BaseBalancingStrategy},
		},
	}
	assert.Equal(t, RatingBalancingStrategy, otherChainCfg.BalancingStrategyFor(chains.ETHEREUM.String()))
}

func TestBalancingStrategyForPerChainOverride(t *testing.T) {
	// A per-chain override wins over the global default; a chain-defaults entry
	// without the field inherits the global value.
	cfg := &UpstreamConfig{
		BalancingStrategy: BaseBalancingStrategy,
		ChainDefaults: map[string]*ChainDefaults{
			chains.ETHEREUM.String(): {BalancingStrategy: RatingBalancingStrategy},
			chains.POLYGON.String():  {}, // no override -> inherits global
		},
	}
	assert.Equal(t, RatingBalancingStrategy, cfg.BalancingStrategyFor(chains.ETHEREUM.String()))
	assert.Equal(t, BaseBalancingStrategy, cfg.BalancingStrategyFor(chains.POLYGON.String()))
}

func TestBalancingStrategyValidate(t *testing.T) {
	// Empty is treated as "inherit/default" and is valid.
	assert.NoError(t, BalancingStrategy("").validate())
	assert.NoError(t, RatingBalancingStrategy.validate())
	assert.NoError(t, BaseBalancingStrategy.validate())

	err := BalancingStrategy("bogus").validate()
	assert.ErrorContains(t, err, "invalid balancing strategy - 'bogus'")
}
