package caps

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
)

// WsPresenceCapDetector asserts a single cap while the given websocket connector
// reports a live connection, and retracts it on disconnect. It is the building block
// for WsCap on every chain, and for PendingTxCap on chains that don't gate it behind
// a deeper check (matching the previous "a live ws connector implies the cap"
// behavior).
type WsPresenceCapDetector struct {
	name   string
	cap    protocol.Cap
	wsConn connectors.ApiConnector
}

func NewWsPresenceCapDetector(name string, cap protocol.Cap, wsConn connectors.ApiConnector) *WsPresenceCapDetector {
	return &WsPresenceCapDetector{name: name, cap: cap, wsConn: wsConn}
}

func DefaultCapDetectors(upstreamId string, wsConn connectors.ApiConnector) []CapDetector {
	return []CapDetector{
		NewWsPresenceCapDetector(upstreamId+"_ws_cap", protocol.WsCap, wsConn),
	}
}

func (w *WsPresenceCapDetector) Domain() []protocol.Cap {
	return []protocol.Cap{w.cap}
}

func (w *WsPresenceCapDetector) DetectCaps(ctx context.Context) <-chan mapset.Set[protocol.Cap] {
	out := make(chan mapset.Set[protocol.Cap], 1)

	var stateSub = subscribeStates(w.wsConn, w.name)
	if stateSub == nil {
		// No ws connector (http-only upstream): the cap can never be asserted.
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
				caps := mapset.NewThreadUnsafeSet[protocol.Cap]()
				if state == protocol.WsConnected {
					caps.Add(w.cap)
				}
				select {
				case out <- caps:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}
