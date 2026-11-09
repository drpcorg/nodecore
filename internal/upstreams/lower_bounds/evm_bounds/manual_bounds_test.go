package evm_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEvmManualBoundsForAllTypesSendNoRequests(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	manual := map[protocol.LowerBoundType]int64{
		protocol.StateBound:    1,
		protocol.TraceBound:    1,
		protocol.BlockBound:    1,
		protocol.LogsBound:     1,
		protocol.TxBound:       1,
		protocol.ReceiptsBound: 1,
		protocol.ProofBound:    1,
	}

	detected := lower_bounds.NewGenericLowerBoundProcessorWithDelay(ctx, "id", 0, time.Millisecond, evmCapabilitiesDetectors(connector))
	processor := lower_bounds.WithManualBounds(ctx, "id", manual, detected)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()

	published := make(map[protocol.LowerBoundType]int64)
	for range manual {
		select {
		case data := <-sub.Events:
			published[data.Type] = data.Bound
		case <-time.After(time.Second):
			require.FailNow(t, "timed out waiting for a configured bound")
		}
	}
	assert.Equal(t, manual, published)
	for boundType := range manual {
		assert.Equal(t, int64(1), processor.PredictLowerBound(boundType, 0))
	}

	select {
	case data := <-sub.Events:
		assert.Fail(t, "unexpected bound published", "%+v", data)
	case <-time.After(100 * time.Millisecond):
	}
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}
