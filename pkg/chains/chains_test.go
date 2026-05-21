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
	assert.False(t, IsNoFinalityChain(ethereum.Chain))
}

func TestIsNoFinalityChain_UnknownChainReturnsFalse(t *testing.T) {
	assert.False(t, IsNoFinalityChain(UnknownChain.Chain))
}

func TestIsNoFinalityChain_TrueWhenSettingsFlagSet(t *testing.T) {
	ethereum := GetChain("ethereum")
	assert.NotEqual(t, UnknownChain, ethereum)

	original := ethereum.Settings.NoFinality
	ethereum.Settings.NoFinality = true
	t.Cleanup(func() { ethereum.Settings.NoFinality = original })

	assert.True(t, IsNoFinalityChain(ethereum.Chain))
}
