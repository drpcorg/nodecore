package lower_bounds

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type LowerBoundDetector interface {
	DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error)
	SupportedTypes() []protocol.LowerBoundType
	Period() time.Duration
}

// DecreasingBoundDetector is an optional capability for detectors whose lower
// bound legitimately moves backwards over time (e.g. a ripple archive node
// backfilling history toward genesis). Detectors that implement it with
// AllowsBoundDecrease() == true bypass the monotonic filter in
// BaseLowerBoundProcessor and get every detected bound published as is.
type DecreasingBoundDetector interface {
	AllowsBoundDecrease() bool
}
