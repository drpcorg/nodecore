package connectors

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

// stopDrainTimeout is how long Stop waits for the unary requests that are
// already on a connector before it closes the connection under them.
const stopDrainTimeout = 5 * time.Second

type ObserverConnector struct {
	delegate              ApiConnector
	chain                 chains.Chain
	upstreamId            string
	responseReceivedHooks []protocol.ResponseReceivedHook
	executor              failsafe.Executor[protocol.ResponseHolder]

	// inFlight counts the unary requests between SendRequest and its return
	inFlight     atomic.Int64
	drainTimeout time.Duration
}

func (o *ObserverConnector) GetUrl() string {
	return o.delegate.GetUrl()
}

func (o *ObserverConnector) Unsubscribe(opId string) {
	o.delegate.Unsubscribe(opId)
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
		drainTimeout:          stopDrainTimeout,
	}
}

func (o *ObserverConnector) SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return o.delegate.SubscribeStates(name)
}

func (o *ObserverConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	o.inFlight.Add(1)
	defer o.inFlight.Add(-1)

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

	// there could be internal requests through this connector, so we should add results to the GenericStatsService directly
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

func (o *ObserverConnector) GetType() specs.ApiConnectorType {
	return o.delegate.GetType()
}

func (o *ObserverConnector) Start() {
	o.delegate.Start()
}

// Stop stops the connector of an upstream that is being removed. A websocket or
// gRPC connector owns one connection and closing it fails every call on it, so
// the unary requests that are already in flight get drainTimeout to finish
// first; by this point the upstream is no longer selected, so no new ones
// arrive. Streams and subscriptions never end by themselves and are not waited
// for. HTTP connectors don't interrupt requests on Stop and need no drain.
func (o *ObserverConnector) Stop() {
	if o.closesInFlightRequests() {
		o.drain()
	}
	o.delegate.Stop()
}

func (o *ObserverConnector) closesInFlightRequests() bool {
	connectorType := o.delegate.GetType()
	return connectorType == specs.WebsocketConnector || specs.IsGrpcApiConnectorType(connectorType)
}

func (o *ObserverConnector) drain() {
	if o.inFlight.Load() == 0 {
		return
	}
	timeout := time.NewTimer(o.drainTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for o.inFlight.Load() > 0 {
		select {
		case <-timeout.C:
			log.Warn().Msgf(
				"%d requests are still in flight on the %s connector of upstream %s after %s, closing it anyway",
				o.inFlight.Load(), o.delegate.GetType(), o.upstreamId, o.drainTimeout,
			)
			return
		case <-ticker.C:
		}
	}
}

func (o *ObserverConnector) Running() bool {
	return o.delegate.Running()
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
