package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// All polkadot-family chains share the "polkadot" method spec. Avail switches to
// its own spec only once drpcorg/public carries method-spec: "avail", so it is
// deliberately asserted as "polkadot" here.
func TestPolkadotChainsResolveMethodSpec(t *testing.T) {
	for _, name := range []string{
		"polkadot", "kusama", "vara", "avail", "polymesh",
		"westend", "westend-asset-hub", "paseo", "paseo-asset-hub",
		"polkadot-asset-hub", "zkverify",
	} {
		chain := GetChain(name)
		assert.NotNil(t, chain, "chain %s not configured", name)
		assert.Equal(t, Polkadot, chain.Type, "chain %s is not a polkadot chain", name)
		assert.Equal(t, "polkadot", chain.MethodSpec, "chain %s resolved the wrong method spec", name)
	}
}

func TestPolkadotChainIds(t *testing.T) {
	assert.Equal(t, "polkadot", GetChain("polkadot").ChainId)
	assert.Equal(t, "kusama", GetChain("kusama").ChainId)
	assert.Equal(t, "vara network", GetChain("vara").ChainId)
	assert.Equal(t, "avail network", GetChain("avail").ChainId)
}
