package evm_caps

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

// EvmHeadSubCapDetector asserts NewHeadsCap (and LogsCap) while the head connector is
// a live websocket and the upstream supports the required subscription methods. A ws
// head connector means the head is subscription-driven, so newHeads can be
// synthesized locally; logs additionally needs eth_getLogs. If the head connector is
// not a websocket (SubscribeStates returns nil) the caps are never asserted. Mirrors
// the head-connector branch of the old SubscribeUpstreamStateEvent.
type EvmHeadSubCapDetector struct {
	name     string
	headConn connectors.ApiConnector
	// methods is read on every evaluation rather than captured, so a later method
	// detection round is reflected in the caps this detector asserts.
	methods caps.MethodsSource
}

func NewEvmHeadSubCapDetector(name string, headConn connectors.ApiConnector, methods caps.MethodsSource) *EvmHeadSubCapDetector {
	return &EvmHeadSubCapDetector{name: name, headConn: headConn, methods: methods}
}

func (e *EvmHeadSubCapDetector) Domain() []protocol.Cap {
	return []protocol.Cap{protocol.NewHeadsCap, protocol.LogsCap}
}

func (e *EvmHeadSubCapDetector) DetectCaps(ctx context.Context) <-chan mapset.Set[protocol.Cap] {
	out := make(chan mapset.Set[protocol.Cap], 1)

	var stateSub *utils.Subscription[protocol.SubscribeConnectorState]
	if e.headConn != nil {
		stateSub = e.headConn.SubscribeStates(e.name)
	}
	if stateSub == nil {
		close(out)
		return out
	}

	go func() {
		defer close(out)
		defer stateSub.Unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case state, ok := <-stateSub.Events:
				if !ok {
					return
				}
				select {
				case out <- e.capsFor(state):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

func (e *EvmHeadSubCapDetector) capsFor(state protocol.SubscribeConnectorState) mapset.Set[protocol.Cap] {
	detected := mapset.NewThreadUnsafeSet[protocol.Cap]()

	var upstreamMethods methods.Methods
	if e.methods != nil {
		upstreamMethods = e.methods()
	}

	if state == protocol.WsConnected && upstreamMethods != nil && upstreamMethods.HasMethod("eth_subscribe") {
		detected.Add(protocol.NewHeadsCap)
		if upstreamMethods.HasMethod("eth_getLogs") {
			detected.Add(protocol.LogsCap)
		}
	}
	return detected
}
