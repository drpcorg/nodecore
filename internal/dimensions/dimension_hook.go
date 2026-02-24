package dimensions

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type DimensionHook struct {
	tracker *DimensionTracker
}

func (d *DimensionHook) OnResponseReceived(
	_ context.Context,
	request protocol.RequestHolder,
	_ *protocol.ResponseHolderWrapper,
) {
	go func() {
		for _, result := range request.RequestObserver().GetResults() {
			switch r := result.(type) {
			case *protocol.UnaryRequestResult:
				dims := d.tracker.GetUpstreamDimensions(r.GetChain(), r.GetUpstreamId(), request.Method())

				dims.TrackTotalRequests()
				dims.TrackRequestDuration(r.GetDuration())

				if r.GetRespKind() == protocol.RetryableError {
					dims.TrackTotalErrors()
				}
				if r.IsSuccessfulRetry() {
					dims.TrackSuccessfulRetries()
				}
			}
		}
	}()
}

func NewDimensionHook(tracker *DimensionTracker) *DimensionHook {
	return &DimensionHook{
		tracker: tracker,
	}
}

var _ protocol.ResponseReceivedHook = (*DimensionHook)(nil)
