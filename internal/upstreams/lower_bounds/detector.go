package lower_bounds

import (
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type LowerBoundDetector interface {
	DetectLowerBound() ([]protocol.LowerBoundData, error)
	SupportedTypes() []protocol.LowerBoundType
	Period() time.Duration
}
