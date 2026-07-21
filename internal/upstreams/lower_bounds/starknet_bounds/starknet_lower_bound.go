package starknet_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
)

const starknetPeriod = 15 * time.Minute

// StarknetLowerBoundDetector always publishes bound 1, matching dshackle.
// Starknet clients (juno, pathfinder) keep the full block history - there is
// no block pruning mode - so calculating the bound would always converge to
// the genesis anyway.
type StarknetLowerBoundDetector struct {
	upstreamId string
}

func NewStarknetLowerBoundDetector(upstreamId string) *StarknetLowerBoundDetector {
	return &StarknetLowerBoundDetector{upstreamId: upstreamId}
}

func (s *StarknetLowerBoundDetector) DetectLowerBound(_ context.Context) ([]protocol.LowerBoundData, error) {
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(1, protocol.StateBound),
		protocol.NewLowerBoundDataNow(1, protocol.BlockBound),
	}, nil
}

func (s *StarknetLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound, protocol.BlockBound}
}

func (s *StarknetLowerBoundDetector) Period() time.Duration {
	return starknetPeriod
}

var _ lower_bounds.LowerBoundDetector = (*StarknetLowerBoundDetector)(nil)
