package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every polkadot-family chain resolves the shared "polkadot" method spec. Avail
// is excluded on purpose - see TestAvailUsesPolkadotSpecUntilSubmoduleBump.
func TestPolkadotChainsResolveMethodSpec(t *testing.T) {
	for _, name := range []string{
		"polkadot", "kusama", "vara", "polymesh",
		"westend", "westend-asset-hub", "paseo", "paseo-asset-hub",
		"polkadot-asset-hub", "zkverify",
	} {
		chain := GetChain(name)
		assert.NotNil(t, chain, "chain %s not configured", name)
		assert.Equal(t, Polkadot, chain.Type, "chain %s is not a polkadot chain", name)
		assert.Equal(t, "polkadot", chain.MethodSpec, "chain %s resolved the wrong method spec", name)
	}
}

// Avail ships its own spec (avail.json, with kate_*/mmr_*), but nothing resolves
// to it until drpcorg/public sets method-spec: "avail" on the avail protocol
// block and the submodule is bumped here. This test pins the pre-bump state and
// is expected to be flipped - not deleted - by that follow-up, so the change is
// visible in a test that names it rather than buried in an unrelated loop.
func TestAvailUsesPolkadotSpecUntilSubmoduleBump(t *testing.T) {
	for _, name := range []string{"avail", "avail-testnet"} {
		chain := GetChain(name)
		assert.NotNil(t, chain, "chain %s not configured", name)
		assert.Equal(t, Polkadot, chain.Type, "chain %s is not a polkadot chain", name)
		assert.Equal(t, "polkadot", chain.MethodSpec,
			"chain %s: if this now reports \"avail\", the submodule bump landed - flip this assertion", name)
	}
}

// These are the values PolkadotChainValidator compares system_chain against, so
// they are pinned deliberately (same as pkg/chains/cosmos_chains_test.go).
//
// Confirmed against live nodes: polkadot, kusama, westend, vara, polymesh,
// polkadot-asset-hub and westend-asset-hub all report exactly these strings.
//
// Two do NOT, and are pinned here as the current - wrong - submodule values so
// the mismatch is visible rather than latent. A mismatch is a FatalSettingError,
// so every upstream on these chains is refused at startup until drpcorg/public
// is corrected:
//
//	avail    : chains.yaml "Avail Network"   / node reports "Avail DA Mainnet"
//	zkverify : chains.yaml "ZkVerify Mainnet" / node reports "zkVerify"
func TestPolkadotChainIds(t *testing.T) {
	assert.Equal(t, "polkadot", GetChain("polkadot").ChainId)
	assert.Equal(t, "kusama", GetChain("kusama").ChainId)
	assert.Equal(t, "westend", GetChain("westend").ChainId)
	assert.Equal(t, "vara network", GetChain("vara").ChainId)
	assert.Equal(t, "polymesh mainnet", GetChain("polymesh").ChainId)
	assert.Equal(t, "polkadot asset hub", GetChain("polkadot-asset-hub").ChainId)

	// Known-wrong upstream data - update both sides together when public is fixed.
	assert.Equal(t, "avail network", GetChain("avail").ChainId)
	assert.Equal(t, "zkverify mainnet", GetChain("zkverify").ChainId)
}
