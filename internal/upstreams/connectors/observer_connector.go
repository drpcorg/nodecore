package connectors

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
)

type ObserverConnector struct {
	delegate              ApiConnector
	chain                 chains.Chain
	upstreamId            string
	responseReceivedHooks []protocol.ResponseReceivedHook
	executor              failsafe.Executor[protocol.ResponseHolder]
}

func NewObserverConnector(
	chain chains.Chain,
	upstreamId string,
	delegate ApiConnector,
	responseReceivedHooks []protocol.ResponseReceivedHook,
	executor failsafe.Executor[protocol.ResponseHolder],
) *ObserverConnector {
	return &ObserverConnector{
		chain:                 chain,
		delegate:              delegate,
		upstreamId:            upstreamId,
		executor:              executor,
		responseReceivedHooks: responseReceivedHooks,
	}
}

func (o *ObserverConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	reqObserver := request.RequestObserver()

	executorCtx := context.WithoutCancel(ctx)
	if executorCtx.Value(resilience.RequestKey) == nil {
		executorCtx = context.WithValue(executorCtx, resilience.RequestKey, request)
	}
	// for internal requests we should set chain id explicitly
	if reqObserver.GetChain() == chains.Unknown {
		reqObserver.WithChain(o.chain)
	}

	response, _ := o.executor.
		WithContext(executorCtx).
		GetWithExecution(func(exec failsafe.Execution[protocol.ResponseHolder]) (protocol.ResponseHolder, error) {
			return o.sendRequest(ctx, exec, request)
		})

	// there could be internal requests through this connector, so we should add results to the BaseStatsService directly
	if reqObserver.GetRequestKind() == protocol.InternalUnary {
		for _, hook := range o.responseReceivedHooks {
			hook.OnResponseReceived(ctx, request, &protocol.ResponseHolderWrapper{Response: response})
		}
	}

	return response
}

func (o *ObserverConnector) Subscribe(ctx context.Context, holder protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return o.delegate.Subscribe(ctx, holder)
}

func (o *ObserverConnector) GetType() protocol.ApiConnectorType {
	return o.delegate.GetType()
}

func (o *ObserverConnector) sendRequest(
	ctx context.Context,
	exec failsafe.Execution[protocol.ResponseHolder],
	request protocol.RequestHolder,
) (protocol.ResponseHolder, error) {
	done := request.RequestObserver().TrackUpstreamCall()
	defer done()

	result := protocol.NewUnaryRequestResult()

	now := time.Now()
	responseHolder := o.delegate.SendRequest(ctx, request)
	duration := time.Since(now).Seconds()

	request.RequestObserver().AddResult(
		result.
			WithDuration(duration).
			WithUpstreamId(o.upstreamId).
			WithRespKindFromResponse(responseHolder),
		false,
	)

	if exec.IsRetry() && !responseHolder.HasError() {
		result.WithSuccessfulRetry()
	}

	return responseHolder, nil
}

var _ ApiConnector = (*ObserverConnector)(nil)
