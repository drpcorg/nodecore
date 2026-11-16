package main

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveChain(t *testing.T) {
	for _, name := range []string{"ethereum", "ETHEREUM", "eth"} {
		canonical, chain, ok := resolveChain(name)
		require.True(t, ok, name)
		assert.Equal(t, "ethereum", canonical)
		assert.Equal(t, chains.Ethereum, chain.Type)
	}
	canonical, _, ok := resolveChain("BSC_TESTNET")
	require.True(t, ok)
	assert.Equal(t, chains.GetChain("bsc-testnet").ShortNames[0], canonical)

	for _, name := range []string{"ripple", "kadena", "no-such-chain", ""} {
		_, _, ok := resolveChain(name)
		assert.False(t, ok, name)
	}
}

func TestPrimaryConnectorType(t *testing.T) {
	assert.Equal(t, "rest", primaryConnectorType(chains.EthereumBeaconChain))
	assert.Equal(t, "rest", primaryConnectorType(chains.Algorand))
	assert.Equal(t, "rest", primaryConnectorType(chains.Ton))
	assert.Equal(t, "tendermint", primaryConnectorType(chains.Cosmos))
	for _, family := range []chains.BlockchainType{chains.Ethereum, chains.Solana, chains.Bitcoin, chains.Near} {
		assert.Equal(t, "json-rpc", primaryConnectorType(family))
	}
}
