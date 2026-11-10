package blocks

import (
	"context"
	"testing"
	"time"
)

func TestGenericBlockProcessorStopCancelsInFlightPoll(t *testing.T) {
	specific := newBlockingSpecific()
	// the timeout is far away: only the run's context can end the fetch
	processor := NewGenericBlockProcessor(context.Background(), "up", time.Hour, time.Hour, false, true, &stubConnector{}, specific)

	processor.Start()
	processor.Stop()

	requireCancelled(t, specific, "the in-flight finalized block fetch")
}
