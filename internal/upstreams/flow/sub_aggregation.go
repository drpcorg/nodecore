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

// sourceBuilder picks how the shared source for this subscription is produced:
// locally-synthesized newHeads when the chain has a WS-head-capable upstream, or
// the default node-backed passthrough otherwise.
func sourceBuilder(
	chain chains.Chain,
	supervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
) subengine.SourceBuilder {
	if isNewHeadsRequest(request) && localNewHeadsAvailable(chain, supervisor) {
		return subengine.NewHeadsSourceBuilder(supervisor, chain)
	}
	return newGenericSourceBuilder(supervisor, request, strategy)
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

// subscriptionKey is the aggregation key: subscriptions that share method,
// params (via RequestHash, which is blake2b over method+params) and selector
// routing collapse onto a single upstream source.
func subscriptionKey(request protocol.RequestHolder) string {
	return fmt.Sprintf("%s|%s|%s", request.Method(), request.RequestHash(), selectorKey(request.Selectors()))
}

// selectorKey produces a stable string for a selector tree so that identical
// subscriptions routed the same way collide, while differently-routed ones do
// not. The encoding is deterministic regardless of selector ordering within
// and/or groups.
func selectorKey(selectors []protocol.RequestSelector) string {
	if len(selectors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		parts = append(parts, encodeSelector(selector))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func encodeSelector(selector protocol.RequestSelector) string {
	switch s := selector.(type) {
	case protocol.RequestAnySelector:
		return "any"
	case protocol.RequestLabelSelector:
		values := append([]string(nil), s.Values...)
		sort.Strings(values)
		return fmt.Sprintf("label(%s=%s)", s.Name, strings.Join(values, "|"))
	case protocol.RequestExistsSelector:
		return fmt.Sprintf("exists(%s)", s.Name)
	case protocol.RequestAndSelector:
		return fmt.Sprintf("and(%s)", encodeSelectorGroup(s.Children))
	case protocol.RequestOrSelector:
		return fmt.Sprintf("or(%s)", encodeSelectorGroup(s.Children))
	case protocol.RequestNotSelector:
		return fmt.Sprintf("not(%s)", encodeSelector(s.Child))
	case protocol.RequestHeightSelector:
		return fmt.Sprintf("height(%d)", s.Height)
	case protocol.RequestBlockTagSelector:
		return fmt.Sprintf("tag(%d)", s.Tag)
	case protocol.RequestSlotHeightSelector:
		return fmt.Sprintf("slot(%d)", s.SlotHeight)
	case protocol.RequestLowerHeightSelector:
		return fmt.Sprintf("lower(%d,%d,%d,%d)", s.Height, s.LowerBoundType, s.TimeOffset, s.HeightDelta)
	case protocol.RequestUnsupportedSelector:
		return fmt.Sprintf("unsupported(%s)", s.Reason)
	default:
		return fmt.Sprintf("%T", selector)
	}
}

func encodeSelectorGroup(children []protocol.RequestSelector) string {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		parts = append(parts, encodeSelector(child))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
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
