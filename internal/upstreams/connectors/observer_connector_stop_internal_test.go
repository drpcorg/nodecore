package connectors

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldConnector holds every request until release is closed.
type heldConnector struct {
	connectorType specs.ApiConnectorType
	release       chan struct{}
	entered       chan struct{}
	stopped       atomic.Bool
	// answeredBeforeStop is false if a request was still held when Stop ran
	answeredBeforeStop atomic.Bool
}

func newHeldConnector(connectorType specs.ApiConnectorType) *heldConnector {
	return &heldConnector{connectorType: connectorType, release: make(chan struct{}), entered: make(chan struct{}, 1)}
}

func (b *heldConnector) SendRequest(_ context.Context, _ protocol.RequestHolder) protocol.ResponseHolder {
	b.entered <- struct{}{}
	<-b.release
	b.answeredBeforeStop.Store(!b.stopped.Load())
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"result"`), protocol.JsonRpc)
}

func (b *heldConnector) Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}
func (b *heldConnector) SubscribeStates(string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}
func (b *heldConnector) Unsubscribe(string)              {}
func (b *heldConnector) Start()                          {}
func (b *heldConnector) Stop()                           { b.stopped.Store(true) }
func (b *heldConnector) Running() bool                   { return !b.stopped.Load() }
func (b *heldConnector) GetUrl() string                  { return "" }
func (b *heldConnector) GetType() specs.ApiConnectorType { return b.connectorType }

func observerWithRequestInFlight(t *testing.T, delegate *heldConnector) (*ObserverConnector, chan protocol.ResponseHolder) {
	t.Helper()
	observer := NewObserverConnector(chains.SUI, "id", delegate, nil, resilience.CreateUpstreamExecutor())
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_call", nil, chains.SUI)
	require.NoError(t, err)

	answered := make(chan protocol.ResponseHolder, 1)
	go func() { answered <- observer.SendRequest(context.Background(), request) }()
	select {
	case <-delegate.entered:
	case <-time.After(time.Second):
		t.Fatal("the request didn't reach the connector")
	}
	return observer, answered
}

func TestObserverConnectorStopWaitsForRequestsInFlight(t *testing.T) {
	for _, connectorType := range []specs.ApiConnectorType{specs.GrpcConnector, specs.GrpcAdditional, specs.WebsocketConnector} {
		t.Run(connectorType.String(), func(te *testing.T) {
			delegate := newHeldConnector(connectorType)
			observer, answered := observerWithRequestInFlight(te, delegate)

			stopReturned := make(chan struct{})
			go func() {
				observer.Stop()
				close(stopReturned)
			}()

			// the connection stays open while the request is on it
			time.Sleep(100 * time.Millisecond)
			assert.False(te, delegate.stopped.Load())

			close(delegate.release)
			response := <-answered
			select {
			case <-stopReturned:
			case <-time.After(time.Second):
				te.Fatal("Stop didn't return after the request finished")
			}

			assert.False(te, response.HasError())
			assert.True(te, delegate.answeredBeforeStop.Load())
			assert.True(te, delegate.stopped.Load())
		})
	}
}

func TestObserverConnectorStopGivesUpAfterTheDrainTimeout(t *testing.T) {
	delegate := newHeldConnector(specs.GrpcConnector)
	observer, _ := observerWithRequestInFlight(t, delegate)
	observer.drainTimeout = 100 * time.Millisecond
	defer close(delegate.release)

	started := time.Now()
	observer.Stop()

	assert.True(t, delegate.stopped.Load())
	assert.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond)
	assert.Less(t, time.Since(started), time.Second)
}

// An HTTP connector doesn't interrupt its requests on Stop, so there is nothing
// to wait for.
func TestObserverConnectorStopDoesNotWaitOnHttpConnectors(t *testing.T) {
	for _, connectorType := range []specs.ApiConnectorType{specs.JsonRpcConnector, specs.RestConnector, specs.TendermintConnector} {
		t.Run(connectorType.String(), func(te *testing.T) {
			delegate := newHeldConnector(connectorType)
			observer, answered := observerWithRequestInFlight(te, delegate)

			started := time.Now()
			observer.Stop()

			assert.True(te, delegate.stopped.Load())
			assert.Less(te, time.Since(started), 100*time.Millisecond)

			close(delegate.release)
			assert.False(te, (<-answered).HasError())
		})
	}
}

func TestObserverConnectorStopWithNothingInFlight(t *testing.T) {
	delegate := newHeldConnector(specs.GrpcConnector)
	observer := NewObserverConnector(chains.SUI, "id", delegate, nil, resilience.CreateUpstreamExecutor())

	started := time.Now()
	observer.Stop()

	assert.True(t, delegate.stopped.Load())
	assert.Less(t, time.Since(started), 100*time.Millisecond)
}
