package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cosmos chain with an EVM module carries two ids: the cosmos network name
// and the EVM chain id. chains.yaml declares both under chain-ids, keyed by
// blockchain type, and every family reads its own through ChainIdFor.
func TestChainIdForReadsTheFamilyId(t *testing.T) {
	testnet := GetChain("injective-testnet")
	require.NotEqual(t, UnknownChain, testnet)

	assert.Equal(t, "injective-888", testnet.ChainIdFor(Cosmos))
	assert.Equal(t, "0x59f", testnet.ChainIdFor(Ethereum))
}

// A chain without chain-ids answers its own chain-id for every family.
func TestChainIdForFallsBackToTheChainId(t *testing.T) {
	hub := GetChain("cosmos-hub")
	assert.Equal(t, "cosmoshub-4", hub.ChainIdFor(Cosmos))
	assert.Equal(t, "cosmoshub-4", hub.ChainIdFor(Ethereum))

	ethereum := GetChain("ethereum")
	assert.Equal(t, "0x1", ethereum.ChainIdFor(Ethereum))
}

// net_version is an EVM notion: an explicit net-version wins, otherwise it is
// the decimal of the EVM chain id - the one from chain-ids when declared.
// Injective testnet needs the explicit one, since its net_version (888) is not
// the decimal of its EVM chain id (0x59f = 1439).
func TestNetVersionDerivesFromTheEvmChainId(t *testing.T) {
	assert.Equal(t, "888", GetChain("injective-testnet").NetVersion)
	assert.Equal(t, "1776", GetChain("injective").NetVersion)
	assert.Equal(t, "1", GetChain("ethereum").NetVersion)
}

func TestChainIdForOnALiteralChain(t *testing.T) {
	chain := &ConfiguredChain{ChainId: "0xa"}
	assert.Equal(t, "0xa", chain.ChainIdFor(Ethereum))
}

func TestGetChainByChainIdAndVersionFindsTheEvmSideOfACosmosChain(t *testing.T) {
	found := GetChainByChainIdAndVersion(Ethereum, "0x59f", "888")
	assert.Equal(t, INJECTIVE_TESTNET, found.Chain)

	assert.Equal(t, UnknownChain, GetChainByChainIdAndVersion(Ethereum, "0x59f", "1439"))
}

func TestChainIdsAreLowercased(t *testing.T) {
	loaded, _, err := configureChainsFromBytes([]byte(`
chain-settings:
  protocols:
    - id: injective
      type: cosmos
      settings:
        expected-block-time: 700ms
      chains:
        - id: Mainnet
          chain-ids:
            cosmos: Injective-1
            eth: 0x6F0
          chain-id: injective-1
          short-names: [injective]
          code: INJECTIVE_MAINNET
          grpcId: 1134
`))
	require.NoError(t, err)
	assert.Equal(t, "0x6f0", loaded["injective"].ChainIdFor(Ethereum))
	assert.Equal(t, "injective-1", loaded["injective"].ChainIdFor(Cosmos))
}

func TestChainIdsRejectAnUnknownBlockchainType(t *testing.T) {
	_, _, err := configureChainsFromBytes([]byte(`
chain-settings:
  protocols:
    - id: injective
      type: cosmos
      settings:
        expected-block-time: 700ms
      chains:
        - id: Mainnet
          chain-ids:
            evm: 0x6f0
          chain-id: injective-1
          short-names: [injective]
          code: INJECTIVE_MAINNET
          grpcId: 1134
`))
	require.ErrorContains(t, err, "injective")
	require.ErrorContains(t, err, "evm")
}

// chain-ids may repeat the chain's own id, but it must not contradict chain-id.
func TestChainIdsRejectAMismatchWithTheChainId(t *testing.T) {
	_, _, err := configureChainsFromBytes([]byte(`
chain-settings:
  protocols:
    - id: injective
      type: cosmos
      settings:
        expected-block-time: 700ms
      chains:
        - id: Mainnet
          chain-ids:
            cosmos: injective-888
          chain-id: injective-1
          short-names: [injective]
          code: INJECTIVE_MAINNET
          grpcId: 1134
`))
	require.ErrorContains(t, err, "injective-888")
	require.ErrorContains(t, err, "injective-1")
}
