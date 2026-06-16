package subengine

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryGetIsLazyAndPerChain(t *testing.T) {
	r := NewRegistry(context.Background())

	eth := r.Get(chains.ETHEREUM)
	require.NotNil(t, eth)

	// Same chain returns the same engine instance (lazy, cached).
	assert.True(t, eth == r.Get(chains.ETHEREUM), "expected the same engine per chain")

	// A different chain gets its own engine.
	arb := r.Get(chains.ARBITRUM)
	require.NotNil(t, arb)
	assert.False(t, eth == arb, "expected a distinct engine per chain")
}
