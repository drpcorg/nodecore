package connectors

import (
	"context"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog"
	"time"
)

type DimensionTrackerConnector struct {
	delegate   ApiConnector
	chain      chains.Chain
	upstreamId string
	tracker    *dimensions.DimensionTracker
	executor   failsafe.Executor[protocol.ResponseHolder]
}

func NewDimensionTrackerConnector(
	chain chains.Chain,
	upstreamId string,
	delegate ApiConnector,
	tracker *dimensions.DimensionTracker,
	executor failsafe.Executor[protocol.ResponseHolder],
) *DimensionTrackerConnector {
	return &DimensionTrackerConnector{
		chain:      chain,
		delegate:   delegate,
		upstreamId: upstreamId,
		tracker:    tracker,
		executor:   executor,
	}
}

func (d *DimensionTrackerConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	dims := d.tracker.GetUpstreamDimensions(d.chain, d.upstreamId, request.Method())

	executorCtx := context.WithoutCancel(ctx)
	if executorCtx.Value(protocol.RequestKey) == nil {
		executorCtx = context.WithValue(executorCtx, protocol.RequestKey, request)
	}
	now := time.Now()

	response, _ := d.executor.
		WithContext(executorCtx).
		GetWithExecution(func(exec failsafe.Execution[protocol.ResponseHolder]) (protocol.ResponseHolder, error) {
			dims.TrackTotalRequests() // since there cound be retries we should track all such requests

			responseHolder := d.delegate.SendRequest(ctx, request)

			if exec.IsRetry() && !responseHolder.HasError() {
				zerolog.Ctx(ctx).Debug().Msgf("successful retry of %s", request.Method())
				dims.TrackSuccessfulRetries()
			}
			if protocol.IsRetryable(responseHolder) {
				dims.TrackTotalErrors() // since there cound be retries we should track all such errors
			}

			return responseHolder, nil
		})

	duration := time.Since(now).Seconds()
	dims.TrackRequestDuration(duration)

	return response
}

func (d *DimensionTrackerConnector) Subscribe(ctx context.Context, holder protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return d.delegate.Subscribe(ctx, holder)
}

func (d *DimensionTrackerConnector) GetType() protocol.ApiConnectorType {
	return d.delegate.GetType()
}

var _ ApiConnector = (*DimensionTrackerConnector)(nil)
