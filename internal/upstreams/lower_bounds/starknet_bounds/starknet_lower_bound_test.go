package starknet_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/starknet_bounds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStarknetLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	detector := starknet_bounds.NewStarknetLowerBoundDetector("id")

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.StateBound,
			protocol.BlockBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 15*time.Minute, detector.Period())
}

func TestStarknetLowerBoundDetector_AlwaysPublishesBoundOne(t *testing.T) {
	detector := starknet_bounds.NewStarknetLowerBoundDetector("id")

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	got := make(map[protocol.LowerBoundType]int64, len(result))
	for _, b := range result {
		got[b.Type] = b.Bound
	}
	assert.Equal(t, int64(1), got[protocol.StateBound])
	assert.Equal(t, int64(1), got[protocol.BlockBound])
}
