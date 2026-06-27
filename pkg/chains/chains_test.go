package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChainByGrpcId(t *testing.T) {
	ethereum := GetChain("ethereum")
	assert.NotNil(t, ethereum)
	assert.NotEqual(t, UnknownChain, ethereum)

	byGrpc := GetChainByGrpcId(ethereum.GrpcId)
	assert.Equal(t, ethereum.Chain, byGrpc.Chain)
	assert.Equal(t, ethereum.MethodSpec, byGrpc.MethodSpec)
	assert.Equal(t, ethereum.ShortNames[0], byGrpc.ShortNames[0])
}

func TestGetChainByGrpcIdUnknown(t *testing.T) {
	unknown := GetChainByGrpcId(-1)
	assert.Equal(t, UnknownChain, unknown)
}

func TestGoldLowerBoundsParsedFromConfig(t *testing.T) {
	ethereum := GetChain("ethereum")
	require.NotNil(t, ethereum.LowerBounds)
	require.NotNil(t, ethereum.LowerBounds.Tx)
	require.NotNil(t, ethereum.LowerBounds.Receipts)

	const expectedHash = "0x5c504ed432cb51138bcf09aa5e8a410dd4a1e204ef84bfed1be16dfba1b22060"
	assert.Equal(t, expectedHash, ethereum.LowerBounds.Tx.Hash)
	assert.Equal(t, uint64(46147), ethereum.LowerBounds.Tx.Block)
	assert.Equal(t, expectedHash, ethereum.LowerBounds.Receipts.Hash)
}

func TestGoldLowerBoundsAbsentWhenNotConfigured(t *testing.T) {
	// Bitcoin has no lower-bounds section in chains.yaml.
	assert.Nil(t, GetChain("bitcoin").LowerBounds)
}

func TestAptosBlockchainTypeIsValid(t *testing.T) {
	assert.True(t, IsValidBlockchainType("aptos"))
}

func TestGetChainByChainIdAndVersionScopesByType(t *testing.T) {
	// Ethereum mainnet is registered with chain-id 0x1 / net-version 1.
	eth := GetChainByChainIdAndVersion(Ethereum, "0x1", "1")
	assert.Equal(t, ETHEREUM, eth.Chain)

	// Same chain-id under a different blockchain type must not resolve to it.
	other := GetChainByChainIdAndVersion(Solana, "0x1", "1")
	assert.Equal(t, UnknownChain, other)
}

func TestAptosChainsRegistered(t *testing.T) {
	mainnet := GetChain("aptos-mainnet")
	assert.Equal(t, APTOS, mainnet.Chain)
	assert.Equal(t, Aptos, mainnet.Type)
	assert.Equal(t, "0x1", mainnet.ChainId)
	assert.Equal(t, "1", mainnet.NetVersion)
	assert.Equal(t, 1168, mainnet.GrpcId)
	assert.Equal(t, "aptos", mainnet.MethodSpec)
	assert.Equal(t, mainnet, GetChain("aptos")) // alias
	assert.Equal(t, mainnet, GetChainByGrpcId(1168))

	testnet := GetChain("aptos-testnet")
	assert.Equal(t, APTOS_TESTNET, testnet.Chain)
	assert.Equal(t, "0x2", testnet.ChainId)
	assert.Equal(t, 10203, testnet.GrpcId)
}
