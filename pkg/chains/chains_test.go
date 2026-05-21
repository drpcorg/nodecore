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

func TestIsNoFinalityChain_DefaultsFalseForKnownChain(t *testing.T) {
	ethereum := GetChain("ethereum")
	assert.NotEqual(t, UnknownChain, ethereum)
	assert.False(t, IsNoFinalityChain(ethereum.Chain), "no-finality must default to false for embedded chains")
}

func TestIsNoFinalityChain_UnknownChainReturnsFalse(t *testing.T) {
	assert.False(t, IsNoFinalityChain(UnknownChain.Chain), "unknown chain must not be treated as no-finality")
}

func TestIsNoFinalityChain_TrueWhenSettingsFlagSet(t *testing.T) {
	ethereum := GetChain("ethereum")
	assert.NotEqual(t, UnknownChain, ethereum)

	// Toggle the flag on the in-memory ConfiguredChain to exercise the
	// helper without needing the extra-chains loader (which is a separate
	// concern handled by LoadExtraChains).
	original := ethereum.Settings.NoFinality
	ethereum.Settings.NoFinality = true
	t.Cleanup(func() { ethereum.Settings.NoFinality = original })

	assert.True(t, IsNoFinalityChain(ethereum.Chain))
}
