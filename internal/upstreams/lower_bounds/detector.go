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
