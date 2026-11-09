package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every plain cosmos-family chain shares one method spec: the CometBFT RPC and
// the SDK LCD are the same on all of them, so none of them sets method-spec in
// chains.yaml and the blockchain type supplies the default.
func TestCosmosChainsResolveTheCosmosMethodSpec(t *testing.T) {
	shortNames := []string{
		"cosmos-hub", "cosmos-hub-testnet",
		"axelar", "osmosis", "neutron", "babylon",
		"agoric", "coreum", "fetch-ai", "provenance",
		"initia", "mantra",
	}
	for _, shortName := range shortNames {
		chain := GetChain(shortName)
		assert.NotEqual(t, UnknownChain, chain, shortName)
		assert.Equal(t, Cosmos, chain.Type, shortName)
		assert.Equal(t, "cosmos", chain.MethodSpec, shortName)
	}
}

// A cosmos chain with an EVM module serves the Ethereum JSON-RPC next to the
// cosmos endpoints and points at the cosmos-evm bundle instead.
func TestCosmosEvmChainsResolveTheCosmosEvmMethodSpec(t *testing.T) {
	for _, shortName := range []string{"injective", "injective-testnet"} {
		chain := GetChain(shortName)
		assert.Equal(t, Cosmos, chain.Type, shortName)
		assert.Equal(t, "cosmos-evm", chain.MethodSpec, shortName)
	}
}

// Cosmos chain ids are opaque strings, not hex numbers - the chain validators
// compare them literally rather than parsing them.
func TestCosmosChainIdsAreOpaqueStrings(t *testing.T) {
	assert.Equal(t, "cosmoshub-4", GetChain("cosmos-hub").ChainIdFor(Cosmos))
	assert.Equal(t, "osmosis-1", GetChain("osmosis").ChainIdFor(Cosmos))
	assert.Equal(t, "injective-1", GetChain("injective").ChainIdFor(Cosmos))
}

func TestCosmosBlockchainTypeIsValid(t *testing.T) {
	assert.True(t, IsValidBlockchainType("cosmos"))
	assert.Equal(t, "cosmos", GetMethodSpecNameByChain(COSMOS_HUB))
}
