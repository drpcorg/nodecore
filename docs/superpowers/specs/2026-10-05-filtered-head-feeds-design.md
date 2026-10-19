# Filtered head feeds

**Date:** 2026-10-05
**Branch:** `sub_selectors`
**Status:** design approved in discussion, ready for an implementation plan

## Problem

`GenericChainSupervisor` runs one `HeightForkChoice` over every upstream of a chain and
publishes the winner as a `HeadWrapper` on the `SubscribeState` channel. There is exactly one
merged head per chain, and every consumer sees the same one.

That is wrong for the locally synthesized EVM subscriptions, which can only use a subset of the
upstreams:

- `newHeads` forwards the head's `RawData`, which only a ws-subscription head carries. When the
  fork-choice winner is a polled-head upstream the block is silently skipped.
- `logs` fetches `eth_getLogs {blockHash}` for every head. When the winner is an upstream without
  `eth_getLogs`, no eligible upstream has the block yet and its logs are skipped (80-95% of
  Ethereum blocks on drpc-core once `eth_getLogs` was disabled on the faster upstream).
- Neither source can honor client selectors (dshackle `Selector`). `newHeads` ignores them;
  `logs` refuses them and falls back to node passthrough.

PR #398 patched only the logs case from the consumer side: `StreamBlockUpdates` ignores the head
event payload, re-reads every `UpstreamState` on each state event and every 50 ms, picks the
highest head among `canServeLogs` upstreams, and backfills jumped-over heights by parent hash.
It is a workaround for the missing primitive, not the primitive.

dshackle has the primitive: `GenericMultistream.getHead(matcher)` builds a `MergedHead` over
the upstreams that match a selector. Its callers are exactly `ConnectNewHeads` and
`ConnectBlockUpdates`. Its membership is a static snapshot at creation time, which is a known
weakness.

## Goal

A general head primitive on `ChainSupervisor`: pass a filter, get a head feed computed over only
the upstreams that pass it, with membership re-evaluated as upstream state changes. Rebuild the
`newHeads` and `logs` sources on it, keyed per selector, and remove the #398 polling.

Out of scope: the global merged head, `SubscribeState`, emerald `SubscribeChainStatus`,
head-lag tracking and `pending_tx_source` keep working exactly as today.

## Design

### 1. API and event types (`internal/upstreams`)

```go
// interfaces.go
SubscribeHead(name string, filter FilterUpstream) *HeadFeedSubscription
```

```go
// state_wrappers.go (next to ChainSupervisorStateWrapper)
type HeadFeedEvent interface{ headFeedEvent() }

// HeadUpdated is the feed's new head and the upstream whose event produced it.
type HeadUpdated struct {
	Head       protocol.Block
	UpstreamId string
}

// HeadFeedEmpty means no upstream passes the filter any more.
type HeadFeedEmpty struct{}

// HeadFeedSubscription is one consumer's view of a feed. Unsubscribe removes the
// feed from the supervisor and closes Events; it is idempotent.
type HeadFeedSubscription struct {
	Events <-chan HeadFeedEvent
	// unexported unsubscribe hook
}
func NewHeadFeedSubscription(events <-chan HeadFeedEvent, unsubscribe func()) *HeadFeedSubscription
```

`HeadFeedSubscription` is a dedicated type rather than `utils.Subscription` because the
supervisor must learn about the unsubscribe to drop the feed and its fork choice;
`utils.Subscription` only removes itself from its manager. `NewHeadFeedSubscription` exists so
test fakes in other packages can hand out feeds.

- `FilterUpstream func(id string, state *protocol.UpstreamState) bool` already exists in
  `interfaces.go`, so the `upstreams` package needs nothing from `flow`.
- `name` is used for logging only; feeds are keyed by identity, so two feeds may share a name.
- A sum type, not a zero-value block, so the empty case is an explicit type-switch arm and the
  feed stays general: a feed may go empty and later resume. Whether empty is terminal is the
  consumer's policy.
- The feed admits an upstream only when `state.Status == protocol.Available` **and** the
  filter passes. Baking in Available mirrors the global head, which also drops non-available
  upstreams in `Choose`, so a feed can never follow a syncing node.
- Each feed owns its own `choice.ForkChoice`. `NewGenericChainSupervisor` takes a factory
  `func() choice.ForkChoice` instead of one instance, so the global head and the feeds use the
  same implementation (`upstream_supervisor.go:171` passes `choice.NewHeightForkChoice`).
- No dedup cache like dshackle's `filteredHeads`. Each engine actor already holds one source
  per aggregation key, and the key carries the selector, so no two callers ask for the same
  filter.

### 2. Event loop mechanics

Everything stays on the `processEvents` goroutine. Feeds live in a map guarded by one mutex,
held around each event's handling and in `SubscribeHead` / unsubscribe. The lock is uncontended
on the hot path and, unlike routing subscribes through `eventsChan`, cannot block on shutdown.

After the existing `upstreamStates` update for an event about upstream `id`, for every feed:

```go
state := b.upstreamStates[id]   // nil after RemoveUpstreamEvent
admitted := state != nil && state.Status == protocol.Available && feed.filter(id, state)
if admitted {
	feed.members[id] = struct{}{}
	updated, head = feed.fc.Choose(id, &HeadUpstreamEvent{Status: Available, Head: state.HeadData})
} else {
	delete(feed.members, id)
	updated, head = feed.fc.Choose(id, &HeadUpstreamEvent{Status: Unavailable})
}
if updated && !head.IsEmptyByHeight() { publish HeadUpdated{head, id} }
if len(feed.members) == 0 && members were non-empty before { publish HeadFeedEmpty{} }
```

`ForkChoice.Choose` keeps its current signature. `HeadUpdated.UpstreamId` is the upstream whose
event triggered the change, the same convention `ChainHeadData.UpstreamId` uses today.

- **Members and head are tracked separately.** `members` is the set of upstreams that are
  Available and pass the filter; the fork choice holds the heads of those members that have
  reported one. `HeadFeedEmpty` fires on the member set going from non-empty to empty, which is
  exactly "no upstream passes the filter any more". Deriving emptiness from the fork choice alone
  would also fire when every member is still there but none has a head yet (a chain right after
  start, or an upstream registered before its first head), and a source would wrongly terminate.
- Re-running the filter on every event for that upstream gives dynamic membership: method bans,
  cap loss on ws disconnect, status flips, removal and recovery all take effect on the next
  event. Height-dependent selectors work too, since the filter also runs on head events.
  `HeightForkChoice` already treats a non-Available event as removal and ignores one for an
  upstream it does not track, so no extra bookkeeping is needed on the head side.
- All four event types go through this step, including `StateUpstreamEvent`. The global head
  today ignores `StateUpstreamEvent` and waits for the next `HeadUpstreamEvent` carrying the new
  status; feeds react immediately. The global head is left as is.
- **Seeding.** `SubscribeHead` ranges `upstreamStates` under the lock and runs the same admit
  step for each. It then sends at most one initial event: `HeadUpdated` when some member has a
  head (dshackle's `AbstractHead.getFlux()` likewise starts with the current head), `HeadFeedEmpty`
  when there are no members, and nothing when members exist but none has reported a head yet.
- **Emission.** Non-blocking send into the subscription's buffered channel (100). A full buffer
  means the subscriber is a whole buffer behind, which is a broken consumer rather than a slow one:
  nothing is dropped, the supervisor closes that feed (with a warning), the same way the
  subscription engine disconnects a too-slow client. Both consumers already treat a closed feed
  as terminal, so clients get a terminal frame and resubscribe onto a fresh source; in particular
  `HeadFeedEmpty`, which nothing follows, can never be lost. The send and every close happen under
  the feeds mutex so they can never race. (dshackle's `AbstractHead.getFlux()` chooses the opposite,
  `onBackpressureLatest`; closing is simpler to reason about and consistent with the engine.)
- **Membership churn that leaves the max unchanged emits nothing**, because `HeightForkChoice`
  only reports `updated` on a height change. A member leaving while another member without a
  head remains emits nothing either; the next head of that member is the next event.
- **Shutdown.** When the supervisor context ends all feeds are closed; consumers exit through
  their existing `!ok` branch.

### 3. Consumers (`internal/upstreams/flow`, `flow/subengine`)

Matcher-to-filter adapter, generalizing the one-liner `canServeLogs` from #398:

```go
func matcherFilter(m Matcher) upstreams.FilterUpstream {
	return func(id string, st *protocol.UpstreamState) bool { return m.Match(id, st).Type() == SuccessType }
}
```

Client selectors are compiled with the existing `buildSelectorRouting` (the returned order is
ignored for feeds). `WsCapMatcher` is generalized to `CapMatcher(cap protocol.Cap)`; its one
existing use becomes `NewCapMatcher(protocol.WsCap)`.

| Source | Feed filter | Aggregation key |
|---|---|---|
| `newHeads` | Available + `CapMatcher(NewHeadsCap)` + client selectors | `local\|newHeads\|<selectorKey>` |
| `logs` | Available + `CapMatcher(LogsCap)` + client selectors | `local\|logs\|<selectorKey>` |

- `LogsCap` already means "`NewHeadsCap` holds and `eth_getLogs` is enabled"
  (`evm_head_sub_detector.go`), so one cap check covers both; no `MethodMatcher("eth_getLogs")`.
- The keys carry the key of the request's routing selectors (`localKey`), so sharing is per
  filter, which is dshackle's behaviour. Selectors that compile to no matcher - `RequestAnySelector`
  and sort hints such as block tags - are left out, since they do not change a feed. The
  `localNewHeadsKey` / `localLogsKey` constants and their "selectors are ignored" comments go. The
  `!hasEffectiveSelectors` gate on logs is removed.
- A malformed logs filter object is rejected up front with `InvalidParamsError` when local logs
  are on, before any upstream check; it is never handed to a node instead.
- Every head now carries `RawData` by construction is **not** true: a websocket head also
  publishes RPC-fetched blocks (`SubscriptionHead.getLatestBlock`) and manual heads without it, so
  the `len(RawData)==0` skip in the newHeads source is a real filter.
- **`newHeads` source** (`subengine/heads.go`): subscribes its feed, forwards `RawData` of each
  `HeadUpdated` that carries `RawData`; heads without it (RPC-fetched or manual blocks of a
  websocket head) are skipped. Terminates with `SubscribeTotalFailureError` on
  `HeadFeedEmpty`. The `newHeadsLost` cap re-read goes, replaced by `HeadFeedEmpty`. The feed's
  initial `HeadUpdated`, when it carries `RawData`, is forwarded like any other, so a source's
  first client receives the current head (dshackle parity; today's source waits for the next
  block). When the feed's head moves down because its owner left, the lower head is forwarded like
  any other, as the global-head-based source did before; clients already dedupe by hash.
- **`logs` source** (`flow/logs_source.go`, `subengine/blockupdates.go`): `StreamBlockUpdates`
  takes the chain and a `*upstreams.HeadFeedSubscription` instead of `ChainSupervisor`, owns
  its Unsubscribe, and is event-driven again: each `HeadUpdated` goes through
  `advanceWithAncestors` (the initial one included, so the current block's logs are fetched at
  build time, as dshackle does); `HeadFeedEmpty` closes `out`, which the source turns into
  `SubscribeTotalFailureError` (today a closed `updates` channel ends the source silently). The fetch side
  (`fetchBlockLogs`, `blockByHashResolver`) is unchanged: rating selection with a
  `HeightMatcher` only, **no client selectors**. Logs are fetched by `blockHash`, so any
  upstream that has the block returns identical content; dshackle's `ProduceLogs` ignores the
  selector there too. The #398 failure cannot recur because the head's own upstream passed the
  feed filter, so it has `eth_getLogs` and has the block, and height-based selection always
  finds at least it.
- **`resolveSource` pre-check** (`localFeedFilter`): with the topic's local flag on, some upstream
  must pass the source's full filter right now (`GetSortedUpstreamIds` with a no-op sort), else
  `resolveSource` returns `NoAvailableUpstreamsError` and the client gets a terminal failure. There
  is no fallback onto the node-backed path for an enabled topic: a chain that cannot serve it
  locally (polled heads, no `NewHeadsCap` anywhere) must have the flag turned off, which is the
  operator's switch. This replaces the earlier HTTP "lenient" auto-detection; it matches gRPC's
  strict mode and dshackle. `resolveSource` returns a `resolvedSource{key, builder, filter}` plus an
  error for this. The chain-wide `Caps` union stays for `SubMethods` advertising; it is not used
  for routing.
- Unsupported selectors compile to `UnsupportedSelectorMatcher`, which fails every upstream, so
  they are the error case above, matching how the unary request path treats them.
### 4. What is removed from #398 and what stays

Removed:

- `bestHead`, the `eligible` parameter, the 50 ms `recheck` ticker and `logsHeadRecheck`, the
  per-event full `GetUpstreamIds` state scan in `StreamBlockUpdates`.
- The one-second `lostCheck` ticker, `logsLostCheck` and `logsLost` in `newLogsSourceBuilder`.
- `logsMatcher` and `canServeLogs`.
- The `nodecore_logs_source_head_lag_blocks` gauge (`headLagMetric`) and its doc entry. It
  measured how far the eligible head trailed the merged head, which validated the workaround;
  per-upstream head lag is already tracked by `calculateHeadLags`.

Kept:

- `needsParent`, `advanceWithAncestors`, `BlockResolver`, `maxBackfillBlocks`,
  `blockByHashResolver` and `nodecore_logs_source_backfill_failed_total`. A filtered feed still
  jumps heights when membership changes, when a ws head skips a block, or when a feed event is
  dropped on a full buffer.
- The not-ready retry path (`notReadyErrors`, `logsNotReadyWait`), which is independent of the
  head source.

### 5. Docs

- `docs/nodecore/13-subscriptions.md`: the `newHeads` row and section (one source per chain,
  merged-head tap) and the `logs` section (eligible-upstream polling, 50 ms) are rewritten
  around filtered feeds and per-selector sharing.
- `docs/nodecore/08-prometheus-metrics.md`: drop `nodecore_logs_source_head_lag_blocks`.

## Testing

- `chain_supervisor` tests for feeds: admit on `HeadUpstreamEvent`; evict on
  `StateUpstreamEvent` status flip, on method ban, on `RemoveUpstreamEvent`; re-admit on
  `ValidUpstreamEvent`; initial `HeadUpdated` seed, initial `HeadFeedEmpty`, and no initial event when members exist
  without a head; `HeadFeedEmpty`
  when the last member leaves and `HeadUpdated` when one returns; two feeds with different
  filters over one event stream; unsubscribe mid-stream; feeds closed on context end.
- `blockupdates` tests: drop the polling cases, feed a channel of `HeadFeedEvent` directly;
  keep the backfill and reorg cases; `HeadFeedEmpty` closes `out`.
- `heads.go` / `logs_source` tests: termination on `HeadFeedEmpty`; `RawData` forwarded from
  `HeadUpdated`; `resolveSource` picks local vs generic by the full filter, keys include the
  selector, and two selectors yield two keys.
- `matchers` test for `CapMatcher`.
