package dimensions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestDimensionsHookMultipleResults(t *testing.T) {
	tracker := dimensions.NewDimensionTracker()
	hook := dimensions.NewDimensionHook(tracker)

	request := protocol.NewUpstreamJsonRpcRequest("1", []byte(`1`), "eth_call", nil, false, nil)
	responseHolder := protocol.NewSimpleHttpUpstreamResponse("1", []byte("res"), protocol.JsonRpc)
	responseHolder1 := protocol.NewTotalFailure(request, protocol.CtxError(errors.New("test error")))
	responseHolder2 := protocol.NewPartialFailure(request, protocol.RequestTimeoutError())
	request.RequestObserver().
		WithRequestKind(protocol.InternalUnary).
		WithChain(chains.POLYGON)

	request.RequestObserver().
		AddResult(
			protocol.NewUnaryRequestResult().
				WithUpstreamId("upId").
				WithDuration(0.5).
				WithRespKindFromResponse(responseHolder),
			false,
		)
	request.RequestObserver().
		AddResult(
			protocol.NewUnaryRequestResult().
				WithUpstreamId("upId2").
				WithDuration(0.8).
				WithRespKindFromResponse(responseHolder2).
				WithSuccessfulRetry(),
			false,
		)
	request.RequestObserver().
		AddResult(
			protocol.NewUnaryRequestResult().
				WithUpstreamId("upId").
				WithDuration(0.11).
				WithRespKindFromResponse(responseHolder1),
			false,
		)

	hook.OnResponseReceived(context.Background(), request, nil)
	time.Sleep(10 * time.Millisecond)

	dims := tracker.GetUpstreamDimensions(chains.POLYGON, "upId", "eth_call")

	assert.Equal(t, uint64(2), dims.GetTotalRequests())
	assert.Equal(t, uint64(0), dims.GetTotalErrors())
	assert.Equal(t, float64(0), dims.GetErrorRate())
	assert.Equal(t, uint64(0), dims.GetSuccessfulRetries())
	assert.True(t, dims.GetValueAtQuantile(0.9) > 0)

	dims = tracker.GetUpstreamDimensions(chains.POLYGON, "upId2", "eth_call")

	assert.Equal(t, uint64(1), dims.GetTotalRequests())
	assert.Equal(t, uint64(1), dims.GetTotalErrors())
	assert.Equal(t, float64(1), dims.GetErrorRate())
	assert.Equal(t, uint64(1), dims.GetSuccessfulRetries())
	assert.True(t, dims.GetValueAtQuantile(0.9) > 0)
}
