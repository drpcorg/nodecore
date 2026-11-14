package chains_test

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestOptionsParseDisableGasLimitValidation(t *testing.T) {
	var options chains.Options

	require.NoError(t, yaml.Unmarshal([]byte("disable-gas-limit-validation: true"), &options))

	require.NotNil(t, options.DisableGasLimitValidation)
	assert.True(t, *options.DisableGasLimitValidation)
}

func TestGasLimitValidationExemptChains(t *testing.T) {
	for _, name := range []string{"avalanche", "avalanche-fuji", "monad-mainnet", "zksync"} {
		options := chains.GetChain(name).Settings.Options
		require.NotNil(t, options, name)
		require.NotNil(t, options.DisableGasLimitValidation, name)
		assert.True(t, *options.DisableGasLimitValidation, name)
	}

	ethereum := chains.GetChain("ethereum").Settings.Options
	if ethereum != nil {
		assert.Nil(t, ethereum.DisableGasLimitValidation)
	}
}
