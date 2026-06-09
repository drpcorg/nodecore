package evm_specific

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchiveCapabilityFalseUsesStaticFalseDetector(t *testing.T) {
	archive := false
	detector := archiveLabelsDetector(&EvmChainSpecificObject{
		chain:   chains.GetChain(chains.ETHEREUM.String()),
		options: &chains.Options{ArchiveCapability: &archive},
	})

	staticDetector, ok := detector.(*labels.StaticLabelsDetector)
	require.True(t, ok)
	assert.Equal(t, map[string]string{"archive": "false"}, staticDetector.DetectLabels())
}

func TestArchiveCapabilityTrueStillUsesRuntimeDetector(t *testing.T) {
	archive := true
	detector := archiveLabelsDetector(&EvmChainSpecificObject{
		chain:   chains.GetChain(chains.ETHEREUM.String()),
		options: &chains.Options{ArchiveCapability: &archive},
	})

	assert.IsType(t, &eth_labels.EthArchiveLabelsDetector{}, detector)
}
