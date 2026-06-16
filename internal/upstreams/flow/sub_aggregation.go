package flow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/google/uuid"
)

// localNewHeadsKey is the aggregation key for the locally-synthesized newHeads
// source. The local source taps the chain's single merged-head stream and
// ignores request selectors, so all local newHeads subscribers must collapse
// onto one source regardless of their selectors (one head tap per chain).
const localNewHeadsKey = "local|newHeads"

// resolveSource decides how the shared source for this subscription is produced
// and returns its aggregation key alongside the builder, keeping the local-vs-
// generic decision and the key in one place:
//   - locally-synthesized newHeads (one source per chain) when the chain has a
//     WS-head-capable upstream, or
//   - the default node-backed passthrough, keyed by method+params+selectors.
func resolveSource(
	chain chains.Chain,
	supervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
) (string, subengine.SourceBuilder) {
	if isNewHeadsRequest(request) && localNewHeadsAvailable(chain, supervisor) {
		return localNewHeadsKey, subengine.NewHeadsSourceBuilder(supervisor, chain)
	}
	return subscriptionKey(request), newGenericSourceBuilder(supervisor, request, strategy)
}

// isNewHeadsRequest reports whether request is eth_subscribe("newHeads"). Only
// EVM chains expose eth_subscribe, so this also implies an EVM chain.
func isNewHeadsRequest(request protocol.RequestHolder) bool {
	if request.Method() != "eth_subscribe" {
		return false
	}
	body, err := request.Body()
	if err != nil {
		return false
	}
	node, err := sonic.Get(body, "params", 0)
	if err != nil {
		return false
	}
	value, err := node.String()
	if err != nil {
		return false
	}
	return value == "newHeads"
}

// localNewHeadsAvailable reports whether the chain can synthesize newHeads
// locally, i.e. some available upstream has a subscription-driven head
// (NewHeadsCap). A json-rpc/rest head connector does not get the cap, so such
// chains correctly fall back to the generic node-backed source.
func localNewHeadsAvailable(chain chains.Chain, supervisor upstreams.UpstreamSupervisor) bool {
	chainSup := supervisor.GetChainSupervisor(chain)
	if chainSup == nil {
		return false
	}
	caps := chainSup.GetChainState().Caps
	return caps != nil && caps.Contains(protocol.NewHeadsCap)
}

// subscriptionKey is the aggregation key: subscriptions that share method and
// params (via RequestHash, which is blake2b over method+params) and selector
// routing collapse onto a single upstream source. RequestHash already covers
// method+params, so the method is not prefixed separately.
func subscriptionKey(request protocol.RequestHolder) string {
	return fmt.Sprintf("%s|%s", request.RequestHash(), selectorKey(request.Selectors()))
}

// selectorKey produces a stable string for a selector tree so that identical
// subscriptions routed the same way collide, while differently-routed ones do
// not. Per-selector encoding is RequestSelector.Key (deterministic regardless
// of ordering within and/or groups).
func selectorKey(selectors []protocol.RequestSelector) string {
	if len(selectors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		parts = append(parts, selector.Key())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// newGenericSourceBuilder builds the default node-backed source: it selects an
// upstream via the strategy, opens a single ws subscription, and normalizes the
// upstream stream - swallowing the upstream's own confirmation frame, surfacing
// errors/disconnects as a terminal frame, and forwarding actual events. Works
// for any chain family since it is spec-driven (the connector is chosen from
// the method's api-connector types).
func newGenericSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
) subengine.SourceBuilder {
	return func(srcCtx context.Context) (*subengine.Source, error) {
		upstreamId, err := strategy.SelectUpstream(request)
		if err != nil {
			return nil, err
		}
		upstream := supervisor.GetUpstream(upstreamId)
		if upstream == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}
		wsConn := getMethodConnector(upstream, request.SpecMethod())
		if wsConn == nil {
			return nil, protocol.NoApiConnectorsError(request.Method())
		}

		subResp, err := wsConn.Subscribe(srcCtx, request)
		if err != nil {
			return nil, err
		}

		var stateChan chan protocol.SubscribeConnectorState
		statesSub := wsConn.SubscribeStates(fmt.Sprintf("subengine_%s_%s_%s_%d", upstreamId, request.Method(), uuid.NewString(), time.Now().UnixNano()))
		if statesSub != nil {
			stateChan = statesSub.Events
		}

		out := make(chan *protocol.WsResponse, 100)
		go func() {
			defer close(out)
			defer func() {
				if statesSub != nil {
					statesSub.Unsubscribe()
				}
			}()
			for {
				select {
				case <-srcCtx.Done():
					return
				case state, ok := <-stateChan:
					if ok && state == protocol.WsDisconnected {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError(), UpstreamId: upstreamId}
						return
					}
				case r, ok := <-subResp.ResponseChan():
					if !ok {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError(), UpstreamId: upstreamId}
						return
					}
					if r.Error != nil {
						r.UpstreamId = upstreamId
						out <- r
						return
					}
					// Swallow the upstream's own subscription confirmation; each
					// client allocates its own client-facing subscription id in the
					// processor, independent of this shared source.
					if r.SubId == "" {
						continue
					}
					r.UpstreamId = upstreamId
					out <- r
				}
			}
		}()

		stop := func() {
			wsConn.Unsubscribe(subResp.OpId())
		}
		return &subengine.Source{Events: out, Stop: stop}, nil
	}
}
