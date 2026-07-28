package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every cosmos-family chain shares one method spec: the CometBFT RPC and the
// SDK LCD are the same on all of them, so none of them sets method-spec in
// chains.yaml and the blockchain type supplies the default.
func TestCosmosChainsResolveTheCosmosMethodSpec(t *testing.T) {
	shortNames := []string{
		"cosmos-hub", "cosmos-hub-testnet",
		"axelar", "osmosis", "neutron", "babylon",
		"agoric", "coreum", "fetch-ai", "provenance",
		"initia", "injective", "mantra",
	}
	for _, shortName := range shortNames {
		chain := GetChain(shortName)
		assert.NotEqual(t, UnknownChain, chain, shortName)
		assert.Equal(t, Cosmos, chain.Type, shortName)
		assert.Equal(t, "cosmos", chain.MethodSpec, shortName)
	}
}

// Cosmos chain ids are opaque strings, not hex numbers - the chain validators
// compare them literally rather than parsing them.
func TestCosmosChainIdsAreOpaqueStrings(t *testing.T) {
	assert.Equal(t, "cosmoshub-4", GetChain("cosmos-hub").ChainId)
	assert.Equal(t, "osmosis-1", GetChain("osmosis").ChainId)
	assert.Equal(t, "injective-1", GetChain("injective").ChainId)
}

func TestCosmosBlockchainTypeIsValid(t *testing.T) {
	assert.True(t, IsValidBlockchainType("cosmos"))
	assert.Equal(t, "cosmos", GetMethodSpecNameByChain(COSMOS_HUB))
}
