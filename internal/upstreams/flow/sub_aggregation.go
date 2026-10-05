package flow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

// localNewHeadsPrefix starts the aggregation key of the locally-synthesized
// newHeads source. The source follows a head feed filtered by the client's
// selectors, so clients with different selectors get different sources; see
// localKey.
const localNewHeadsPrefix = "local|newHeads"

// localLogsPrefix starts the aggregation key of the locally-synthesized logs
// source. All logs subscribers with the same selectors share ONE all-logs source
// (no address/topic filter in the source); per-client filtering happens in the
// processor. Params are therefore not part of the key; see localKey.
const localLogsPrefix = "local|logs"

// localPendingTxKey is the aggregation key for the locally-synthesized
// newPendingTransactions source. It opens eth_subscribe("newPendingTransactions")
// on every ws-capable upstream of the chain, merges them and dedupes by hash, so
// all clients must collapse onto one source regardless of selectors (one mempool
// tap per chain), unlike the per-selector newHeads and logs keys.
const localPendingTxKey = "local|newPendingTransactions"

// localDrpcPendingTxKey is the aggregation key for drpc_pendingTransactions: it
// rides the shared localPendingTxKey hash source and enriches each hash into a
// full transaction object via eth_getTransactionByHash. Per-chain for the same
// reason as localPendingTxKey.
const localDrpcPendingTxKey = "local|drpcPendingTransactions"

// genericSubscriptionBufferSize mirrors dshackle's high-volume subscription
// buffering for shared logs streams. Generic node-backed subscriptions can still
// carry bursty payloads (logs, pending txes), so they need more headroom than
// the subengine default for low-volume local sources.
const genericSubscriptionBufferSize = 4096

// blockSubscribeBufferSize bounds the source buffer for Solana's blockSubscribe,
// whose events are whole blocks - several megabytes each with full transaction
// details. One event arrives per slot, so the buffer only has to absorb client
// write jitter: a subscriber thousands of blocks behind would pin gigabytes and
// never catch up, so it is better cut off early by the subengine.
const blockSubscribeBufferSize = 100

// localKey is the aggregation key of a local source: its prefix plus the key of
// the request's routing selectors, so identical filters share one source and
// different filters do not. Selectors that compile to no matcher (sort hints
// such as a block tag) order the generic path's candidates but do not filter a
// head feed, so they are left out. Params are not part of it either: the local
// sources carry the whole chain and per-client filtering happens in the
// processor.
func localKey(prefix string, request protocol.RequestHolder) string {
	routing := make([]protocol.RequestSelector, 0, len(request.Selectors()))
	for _, selector := range request.Selectors() {
		if matcher, _ := compileSelector(selector, nil); matcher != nil {
			routing = append(routing, selector)
		}
	}
	return prefix + "|" + selectorKey(routing)
}

// localSourceFilter admits the upstreams a local source may follow: available,
// advertising cap, and matching the client's selectors. topic names the
// subscription in match traces.
func localSourceFilter(
	cap protocol.Cap,
	topic string,
	request protocol.RequestHolder,
	supervisor upstreams.UpstreamSupervisor,
	chainSup upstreams.ChainSupervisor,
) upstreams.FilterUpstream {
	matchers := []Matcher{NewStatusMatcher(), NewCapMatcher(cap, topic)}
	selectorMatchers, _ := buildSelectorRouting(request.Selectors(), supervisor, chainSup)
	matchers = append(matchers, selectorMatchers...)
	return matcherFilter(NewMultiMatcher(matchers...))
}

// hasUpstream reports whether some upstream of the chain passes filter right
// now - the same question the head feed answers, asked before building a source
// so a request fails up front instead of building a source that terminates at
// once.
func hasUpstream(chainSup upstreams.ChainSupervisor, filter upstreams.FilterUpstream) bool {
	noOrder := func(_, _ lo.Tuple2[string, *protocol.UpstreamState]) int { return 0 }
	return len(chainSup.GetSortedUpstreamIds(filter, noOrder)) > 0
}

// localFeedFilter builds the head-feed filter of a local source and checks that
// some upstream passes it right now. When none does - the chain has no upstream
// at all, none advertises cap (a polled-head chain), all capable ones are
// unavailable, or the client's selectors exclude them - the subscription fails
// with NoAvailableUpstreamsError. Local subscriptions are opt-out per chain
// (local-subscriptions in chain-defaults); with them on, a client is served
// locally or not at all, never silently rerouted to a node it did not select
// or whose head is polled. A chain that cannot serve a topic locally must have
// the topic's flag turned off.
func localFeedFilter(
	cap protocol.Cap,
	topic string,
	chain chains.Chain,
	request protocol.RequestHolder,
	supervisor upstreams.UpstreamSupervisor,
) (upstreams.FilterUpstream, error) {
	chainSup := supervisor.GetChainSupervisor(chain)
	if chainSup == nil {
		return nil, protocol.NoAvailableUpstreamsError()
	}
	filter := localSourceFilter(cap, topic, request, supervisor, chainSup)
	if !hasUpstream(chainSup, filter) {
		return nil, protocol.NoAvailableUpstreamsError()
	}
	return filter, nil
}

// resolvedSource is how the shared source for a subscription is produced: its
// aggregation key, its builder, and the per-client filter applied to its events
// (nil when the source emits only what the client asked for).
type resolvedSource struct {
	key     string
	builder subengine.SourceBuilder
	filter  SubFilter
}

// resolveSource decides how the shared source for this subscription is produced,
// keeping the local-vs-generic decision and the key in one place:
//   - locally-synthesized newHeads (one source per selector) over the available
//     upstreams with a subscription-driven head that match the client's
//     selectors, or
//   - locally-synthesized logs (one source per selector) over the available
//     upstreams with LogsCap that match the client's selectors, or
//   - locally-aggregated newPendingTransactions/drpc_pendingTransactions (one
//     source per chain) when the chain has a ws-capable upstream, or
//   - the default node-backed passthrough, keyed by method+params+selectors.
//
// With local newHeads or logs enabled for the chain, no matching upstream right
// now is an error rather than a fallback (see localFeedFilter).
func resolveSource(
	chain chains.Chain,
	supervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
	registry *rating.RatingRegistry,
	engine subengine.Engine,
	settings config.LocalSubSettings,
) (resolvedSource, error) {
	if settings.NewHeads && isNewHeadsRequest(request) {
		filter, err := localFeedFilter(protocol.NewHeadsCap, "newHeads", chain, request, supervisor)
		if err != nil {
			return resolvedSource{}, err
		}
		return resolvedSource{key: localKey(localNewHeadsPrefix, request), builder: subengine.NewHeadsSourceBuilder(supervisor, chain, filter)}, nil
	}
	if settings.Logs && isLogsRequest(request) {
		// the request is checked before the upstreams: a malformed filter object
		// is the client's mistake whatever the chain looks like
		logFilter, err := parseLogFilter(request)
		if err != nil {
			return resolvedSource{}, protocol.InvalidParamsError(err.Error())
		}
		filter, err := localFeedFilter(protocol.LogsCap, "logs", chain, request, supervisor)
		if err != nil {
			return resolvedSource{}, err
		}
		return resolvedSource{key: localKey(localLogsPrefix, request), builder: newLogsSourceBuilder(supervisor, chain, registry, filter), filter: logFilter}, nil
	}
	if settings.PendingTx && isPendingTxRequest(request) && localPendingTxAvailable(chain, supervisor) {
		return resolvedSource{key: localPendingTxKey, builder: newPendingTxSourceBuilder(supervisor, chain)}, nil
	}
	// drpc_pendingTransactions is synthetic (no node-backed equivalent) and stays
	// local regardless of settings; it builds its own pending-tx source internally.
	if isDrpcPendingTxRequest(request) && localPendingTxAvailable(chain, supervisor) {
		return resolvedSource{key: localDrpcPendingTxKey, builder: newDrpcPendingTxSourceBuilder(supervisor, chain, engine)}, nil
	}
	if isGrpcStream(request) {
		// TEMPORARY: gRPC streams are pure pass-through for now. The uuid suffix
		// makes the key unique per request so the engine never shares one
		// upstream stream between clients (a late joiner of a finite List*
		// stream would miss its first messages), while its fan-out and
		// slow-consumer handling stay in force. Delete this branch when
		// aggregation of gRPC streams is implemented: subscriptions then fall
		// through to the shared subscriptionKey below (finite streams must
		// still never be shared).
		return resolvedSource{key: fmt.Sprintf("%s|%s", subscriptionKey(request), uuid.NewString()), builder: newGenericSourceBuilder(supervisor, request, strategy)}, nil
	}
	return resolvedSource{key: subscriptionKey(request), builder: newGenericSourceBuilder(supervisor, request, strategy)}, nil
}

// isGrpcStream reports whether request is a gRPC server-streaming call of
// either kind.
func isGrpcStream(request protocol.RequestHolder) bool {
	return request.SpecMethod() != nil && request.SpecMethod().GrpcCallType().IsServerStream()
}

// isFiniteGrpcStream reports a bounded gRPC stream: a clean upstream close is
// completion, not a failure.
func isFiniteGrpcStream(request protocol.RequestHolder) bool {
	return request.SpecMethod() != nil && request.SpecMethod().GrpcCallType() == specs.GrpcCallTypeServerStreamFinite
}

// subscribeTopic returns the first param of an eth_subscribe request (the topic,
// e.g. "newHeads"/"logs"/"newPendingTransactions"), or ("", false) if request is
// not eth_subscribe or has no string first param. Only EVM chains expose
// eth_subscribe, so a non-empty topic also implies an EVM chain.
func subscribeTopic(request protocol.RequestHolder) (string, bool) {
	if request.Method() != "eth_subscribe" {
		return "", false
	}
	body, err := request.Body()
	if err != nil {
		return "", false
	}
	node, err := sonic.Get(body, "params", 0)
	if err != nil {
		return "", false
	}
	value, err := node.String()
	if err != nil {
		return "", false
	}
	return value, true
}

// isNewHeadsRequest reports whether request is eth_subscribe("newHeads").
func isNewHeadsRequest(request protocol.RequestHolder) bool {
	topic, ok := subscribeTopic(request)
	return ok && topic == "newHeads"
}

// isLogsRequest reports whether request is eth_subscribe("logs", ...).
func isLogsRequest(request protocol.RequestHolder) bool {
	topic, ok := subscribeTopic(request)
	return ok && topic == "logs"
}

// isPendingTxRequest reports whether request is
// eth_subscribe("newPendingTransactions").
func isPendingTxRequest(request protocol.RequestHolder) bool {
	topic, ok := subscribeTopic(request)
	return ok && topic == "newPendingTransactions"
}

// isDrpcPendingTxRequest reports whether request is
// eth_subscribe("drpc_pendingTransactions") - the dRPC variant that enriches each
// pending hash into a full transaction object.
func isDrpcPendingTxRequest(request protocol.RequestHolder) bool {
	topic, ok := subscribeTopic(request)
	return ok && topic == "drpc_pendingTransactions"
}

// localPendingTxAvailable reports whether the chain can aggregate pending-tx
// subscriptions locally, i.e. some available upstream has a live ws connector
// (PendingTxCap). Chains without it fall back to the generic node-backed source.
func localPendingTxAvailable(chain chains.Chain, supervisor upstreams.UpstreamSupervisor) bool {
	chainSup := supervisor.GetChainSupervisor(chain)
	if chainSup == nil {
		return false
	}
	caps := chainSup.GetChainState().Caps
	return caps != nil && caps.Contains(protocol.PendingTxCap)
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
// of ordering within and/or groups). RequestAnySelector has no routing effect
// (compileSelector yields no matcher for it), so it is left out: a request
// carrying it routes, and therefore shares a source, like one without selectors.
func selectorKey(selectors []protocol.RequestSelector) string {
	parts := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		if _, ok := selector.(protocol.RequestAnySelector); ok {
			continue
		}
		parts = append(parts, selector.Key())
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// newGenericSourceBuilder builds the default node-backed source: it selects an
// upstream via the strategy, opens a single upstream subscription/stream via the
// method's connector, and normalizes the upstream stream - surfacing
// errors/disconnects as a terminal frame and forwarding actual events (the
// connector's own service frames, e.g. the ws subscribe confirmation, never
// leave the transport layer). An end frame completes a finite gRPC stream but
// fails a subscription. Works for any chain family since it is
// spec-driven (the connector is chosen from the method's api-connector types).
func newGenericSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
) subengine.SourceBuilder {
	finite := isFiniteGrpcStream(request)
	exclusive := isGrpcStream(request) // per-request key, see resolveSource
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

		bufferSize := genericSourceBufferSize(request)
		out := make(chan protocol.SubResponse, bufferSize)
		// emit never parks on a full buffer once the engine has stopped reading
		// (after terminate nothing drains out; srcCtx is cancelled instead)
		emit := func(r protocol.SubResponse) bool {
			select {
			case out <- r:
				return true
			case <-srcCtx.Done():
				return false
			}
		}
		failure := func() {
			emit(&protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError(), UpstreamId: upstreamId})
		}
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
						failure()
						return
					}
				case r, ok := <-subResp.ResponseChan():
					if !ok {
						failure()
						return
					}
					if r.IsEnd() && !finite {
						// a node ending a live subscription is a failure; only a
						// bounded stream completes
						failure()
						return
					}
					if !emit(r) || r.GetError() != nil || r.IsEnd() {
						return
					}
				}
			}
		}()

		stop := func() {
			wsConn.Unsubscribe(subResp.OpId())
		}
		return &subengine.Source{Events: out, Stop: stop, Buffer: bufferSize, Exclusive: exclusive}, nil
	}
}

// genericSourceBufferSize picks the source and per-subscriber buffer depth for a
// node-backed subscription. Both are counted in events, so methods whose events
// are huge get a smaller depth to keep the retained bytes bounded.
func genericSourceBufferSize(request protocol.RequestHolder) int {
	if request.Method() == "blockSubscribe" {
		return blockSubscribeBufferSize
	}
	return genericSubscriptionBufferSize
}
