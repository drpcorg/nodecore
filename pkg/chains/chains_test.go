package chains

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
