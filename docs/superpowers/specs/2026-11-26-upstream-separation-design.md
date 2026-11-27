# Upstream separation: per-upstream status stream and upstream selector

**Date:** 2026-11-26
**Branches:** `separation_impl` (nodecore), `separation_api` (`drpcorg/public`)
**Status:** design agreed in discussion, ready for an implementation plan

## Problem

The emerald gRPC API shows a client one merged state per chain (`SubscribeChainStatus`) and
nodecore picks the upstream for every `NativeCall` itself. A client that wants to balance across
the upstreams behind a nodecore on its own cannot do it: it does not see the individual
upstreams, and it has no way to say "this attempt goes to that upstream and to no other".

An earlier attempt at this put per-subscriber workers, a throttle, a revision counter and a
request/reply channel into the chain supervisor, mixed queued event payloads with out-of-band
snapshots, and encoded the upstream choice as a label with a magic name. This design replaces it.

## Goals

1. A new server-streaming RPC, `SubscribeUpstreamStatus`, reports every upstream of every chain
   on its own. It has its own request and response messages, which copy the fields of the
   `SubscribeChainStatus` ones and add what the new stream needs.
2. A new selector names one upstream instance by its id and its index. A request carrying it is
   served by that instance or fails; nothing else is tried.
3. The stream never replaces newer information with older information, and it repairs itself
   after a lost internal event within one resync interval.
4. Stream and selector ship together.

## Non-goals

- `SubscribeChainStatus` and its request and response messages are not changed.
- No "removed" kind: a removed upstream is reported as unavailable.
- No new error field. A request whose selector matches nothing gets the existing
  `no available upstreams` error.
- No nodecore config option. The stream and the selector are always available on the emerald API.
- The response cache is not changed: the selector decides which upstream may serve, not what the
  answer is, so it is not part of the cache key.
- The local pending-transaction subscription sources keep ignoring upstream restrictions.
- No grouping of upstreams, no hashing, no label-based partitioning.

## Anti-goals

- No per-subscriber state, timers or goroutines in the chain supervisor. It publishes changes
  and answers reads; everything per stream lives in the emerald layer.
- No second copy of the per-upstream state: no extra map, and the update rules are not written
  again outside the chain supervisor.

## Approaches

**Where the per-upstream feed lives.**

| Option | Trade-off |
|--------|-----------|
| `GenericUpstreamSupervisor` (one loop for all chains) | One subscription and one loop for the stream. But it needs a second `id -> state` map with the chain supervisor's update rules written again, its own copy of the comparison, and the chain's subscription methods resolved again. All chains share one event buffer. |
| **`GenericChainSupervisor` (chosen)** | The old state, the comparison helpers and the chain's subscription methods are already there; the reported state is the one routing reads. Each chain has its own loop and its own buffer. The stream has to follow the chains, as `SubscribeChainStatus` already does. |
| No events: the stream polls the states every interval | Simplest, nothing to lose or reorder. But every head waits up to one interval and it cannot work with an interval of 0. |

**Who works out what changed.** The chain supervisor compares the old and the new state of the
upstream and publishes only the changes, the way `updateState` does for the chain. The
alternative, publishing whole states and comparing in the stream, was dropped in favour of
keeping one pattern in the supervisor.

**Where a first Full gets its content.** An event carries only changes, and the first response
about an upstream needs everything. The stream reads that one upstream from the supervisor when
it needs the Full, the way `SubscribeChainStatus` builds its Full from `GetChainState()`. Two
alternatives were dropped: carrying the whole description in every event (a second payload next
to the changes), and collecting the pieces in the stream (a dropped event would produce an
incomplete Full).

**Revision.** One counter per chain supervisor, shared by all its upstreams, guarded by a plain
mutex. Per-upstream counters were dropped: once a removed upstream's entry is deleted there is
no number left to order a late event against, and a re-added id would restart at 1.

## Key decisions

| # | Decision | Why |
|---|----------|-----|
| 1 | New RPC with its own `SubscribeUpstreamStatusRequest` and `SubscribeUpstreamStatusResponse`; the response copies the chain-status fields and adds `upstream_id` and `upstream_index` | The old messages stay untouched and carry no field that only another RPC reads. The description inside is the same `ChainDescription`, so a client keeps its event handling. An older server answers `Unimplemented`. |
| 2 | A removed upstream is reported unavailable once, then forgotten | Same signal as an outage, no new kind. Its entry leaves the supervisor, so there is nothing to repeat. |
| 3 | The first response for an upstream waits for its head | Same rule as `SubscribeChainStatus`. |
| 4 | Resync every minute, fixed. It carries the six metadata events; the head is added only when it differs from the last head sent for that upstream | An unchanged head must never be sent again: a client stamps every head it receives with an arrival time and counts it as a head event. A head whose own event was skipped or lost still has to reach the client. |
| 5 | `min_interval_ms` in the request throttles per upstream; 0 turns it off | A quiet upstream's update goes out at once; only bursts are merged. |
| 6 | The supervisor publishes changes; the stream owns throttle, resync and what was sent | Layering. |
| 7 | One shared revision per chain supervisor, plain mutex | See Approaches. |
| 8 | The selector is a new member of the `Selector` oneof with id and index, both required | The index only grows, so a replaced upstream keeps its id but gets a new index; the pair names one instance. |
| 9 | The selector is an ordinary matcher; no new strategy | One admitted upstream leaves retries, hedges, fan-out and integrity re-checks nothing else to pick. |
| 10 | A request with the selector is not routed by `label-balancing` | Same rule as the routing headers: the caller named the upstream. |

## Design

### 1. API (`drpcorg/public`, `proto/blockchain.proto`)

```proto
service Blockchain {
    rpc SubscribeUpstreamStatus (SubscribeUpstreamStatusRequest) returns (stream SubscribeUpstreamStatusResponse) {}
}

message SubscribeUpstreamStatusRequest {
    // 0 sends every change at once; a positive value is the minimum time between two
    // responses about the same upstream.
    uint32 min_interval_ms = 1;
}

// One upstream of one chain. Fields 1-3 are the ones of SubscribeChainStatusResponse, with
// the same numbers and meaning.
message SubscribeUpstreamStatusResponse {
    ChainDescription chain_description = 1;
    BuildInfo build_info = 2;
    bool full_response = 3;
    // Set on every response.
    string upstream_id = 4;
    string upstream_index = 5;
}

message Selector {
    oneof selector_type {
        // ... existing members 1-10
        UpstreamSelector upstream_selector = 11;
    }
}

// Admits exactly one upstream instance: both fields must match.
message UpstreamSelector {
    string upstream_id = 1;
    string upstream_index = 2;
}
```

`SubscribeChainStatusRequest` and `SubscribeChainStatusResponse` are not touched.

The change is made on the branch `separation_api` of `drpcorg/public`, created from its `main`;
nothing from the earlier attempt's branch is reused. The stubs under `pkg/dshackle` are regenerated, a release is tagged,
and nodecore's `go.mod` moves to that tag (never to an untagged commit).

### 2. Stream contract

Every response describes one upstream of one chain and carries `upstream_id` and
`upstream_index`.

| Kind | Content | When |
|------|---------|------|
| Full | `full_response = true`, `build_info`, all seven events: status, methods, lower bounds, head, blocks, subscriptions, labels | First time the stream tells the client about this upstream instance |
| Delta | Only the events that changed | Any later change |
| Resync | The six events other than the head; plus the head only when it differs from the last head sent for this upstream | Every minute, for every announced upstream |
| Unavailable | One status event, unavailable | The upstream was removed; sent once |

The events are the existing `ChainEvent`s built by the existing mappers from a one-upstream
view (section 3). Labels go out as one node with quorum 1. Status is the upstream's own final
status, including the lag verdict.

A Delta may carry the head together with other events: changes merged inside one throttle
window go out as one response. A client of this RPC must apply every event of a response.

An upstream without a head is not announced. A chain whose gRPC id is 0 is skipped.

An upstream replaced under the same id (config reload) arrives as a Full with the new index. If
the replacement has no head yet when the removal is due, the client first gets Unavailable and
the Full follows with the head.

### 3. Chain supervisor: the upstream state feed (`internal/upstreams`)

A new file next to `head_feed.go`. The supervisor gains, as one group of fields: a mutex, a
`uint64` revision and a subscription manager. There is no second map: the feed works on the
existing `upstreamStates`.

```go
// UpstreamView is one upstream as the stream reports it: its own state plus the one thing
// the state does not hold.
type UpstreamView struct {
	State      *protocol.UpstreamState // the pointer upstreamStates holds; never modified
	SubMethods mapset.Set[string]      // derived by processSubMethods from its methods and caps
}

// UpstreamStateWrapper is one changed part of an upstream. It is the chain-level wrapper
// type under a name that fits an upstream; the wrapper structs are shared.
type UpstreamStateWrapper = ChainSupervisorStateWrapper

type UpstreamStateEvent struct {
	UpstreamId    string
	UpstreamIndex string
	Revision      uint64
	Removed       bool
	Changes       []UpstreamStateWrapper // only what this event changed
}

type UpstreamViewsSnapshot struct {
	Revision uint64
	Views    map[string]UpstreamView
}

// UpstreamStateFeed is the per-upstream side of one chain: its changes and reads of it.
type UpstreamStateFeed interface {
	SubscribeUpstreamStates(name string) *utils.Subscription[*UpstreamStateEvent]
	// every upstream of the chain, for the start of a stream and for a resync
	GetUpstreamViews() UpstreamViewsSnapshot
	// one upstream, for its first Full; false when the supervisor has no such upstream
	GetUpstreamView(upstreamId string) (UpstreamView, uint64, bool)
}
```

`UpstreamStateFeed` is a separate interface. The `ChainSupervisor` interface is not changed, so
its test stubs are not touched. `GenericChainSupervisor` implements both, with a compile-time
assertion (`var _ UpstreamStateFeed = (*GenericChainSupervisor)(nil)`). The stream obtains the
feed of a chain with a type assertion on its `ChainSupervisor` (section 4).

An event carries only the changes, like the chain-level `ChainSupervisorStateWrapperEvent`, plus
the id, the index and the revision. `UpstreamStateWrapper` is a type alias, so the changes are
the existing wrapper structs and the stream's wrapper-to-proto code takes them as they are:
status, methods, blocks, lower bounds, labels (one entry, amount 1), subscription methods and
head. The feed never produces a caps wrapper; a caps change shows as a subscription-methods
change.

`ChainSupervisorState` and its `Compare` are not used for an upstream and are not changed. The
comparison of two upstream states is its own function in the new file, kind by kind. A function
that turns a view into the full list of wrappers serves a new upstream's first event, and the
stream's Full and resync.

One private helper replaces the four direct writes to `upstreamStates` in `processEvents`
(State, Valid, Head, Remove):

1. Load the upstream's old state from `upstreamStates`; it may be absent.
2. Lock, store the new state (or delete it on removal), increment the revision, unlock.
3. Work out the changes between the old and the new state. A head event changes only the head
   and skips the comparison. An absent old state yields every kind. Subscription methods are
   derived for both sides only when the methods or the caps changed.
4. Publish the event if there are changes, or if a known upstream was removed. It carries the
   revision of its store.

A removal of an upstream the supervisor does not hold does nothing, as today.

`GetUpstreamViews` locks, collects the state pointers from `upstreamStates`, copies the revision
and unlocks; the subscription methods are derived after the unlock. `GetUpstreamView` does the
same for one upstream. Every write to `upstreamStates` happens under the lock together with the
revision increment, so a read that returns revision R contains every write up to R and none
above it. Matchers and the rest of the supervisor keep reading `upstreamStates` without the
lock, as today.

Nothing slow happens under the lock: no comparison, no publishing, no channel operation.

### 4. Stream: `SubscribeUpstreamStatus` (`internal/server/emerald`)

A new file `sub_upstream_status.go`; the handler in `grpc_blockchain.go` checks the session as
the other RPCs do.

**Shared event building.** The two streams send different response types that wrap the same
`ChainDescription`. The part of `stateWrappersToResponse` that turns a list of wrappers into a
`ChainDescription` is extracted and used by both. The new stream builds every response from
wrappers: a Full from the view's full list (with `full_response` and `build_info`), a resync
from that list without the head, a Delta from an event's changes. The legacy stream's output
does not change.

**Layout.** As in `sub_chain_status.go`: one goroutine per chain supervisor pushes responses
into one channel, and the handler's loop sends them on the stream. Chain supervisors are picked
up at start from `GetChainSupervisors()` and on `AddChainSupervisorEvent`, as the legacy stream
does. A chain already followed is skipped. For each chain supervisor the stream takes its feed with a type assertion
to `upstreams.UpstreamStateFeed`; a supervisor that does not implement it is skipped.

**Per chain.** The goroutine subscribes, then reads the snapshot. It keeps one record per
upstream: announced or not, the index, the last head sent, the time of the last send, what is
pending, and the revision of the last read that covered this upstream. A snapshot covers every
upstream of the chain, including the ones it does not contain; a one-upstream read covers that
upstream.

One rule orders reads against events: **an event at or below the revision of the last read that
covered its upstream is ignored.**

The goroutine waits on four inputs:

| Input | Effect |
|-------|--------|
| Event that the rule above ignores | Nothing |
| Event for an announced upstream, same index | Merged into the upstream's pending: changes by kind, the later one wins; a removal replaces everything pending |
| Event for an upstream that is not announced, or whose index differs from the record's | The stream reads that one upstream (`GetUpstreamView`) and remembers the read's revision. If the view has a head, a Full built from the view becomes pending; if not, nothing is pending and the next event tries again. A removal of an unannounced upstream deletes the record. |
| Resync tick | New snapshot. For an announced upstream with the same index the six metadata kinds are taken from the snapshot, and the head too if it differs from the last head sent; whatever was pending is replaced. An upstream that is not announced, or whose index differs, gets a pending Full if its view has a head. An announced upstream missing from the snapshot becomes a pending removal; an unannounced one is dropped. |
| Throttle timer | Flushes the upstreams that are due |

At start the snapshot is handled like a resync with nothing announced: every upstream with a
head gets its Full.

The read for a first Full can be newer than the event that triggered it. Events for that
upstream still queued behind it are then at or below the read's revision and are ignored, so the
client never sees an older head after the Full.

**Throttle.** With `min_interval_ms = 0` every pending upstream is flushed at once. Otherwise,
per upstream: if at least the interval has passed since its last send it is flushed now; if not
it joins a waiting set and one timer per goroutine is armed for the earliest due time. When the
timer fires, the due upstreams are flushed and the timer is armed again for the rest. The time
of the last send is the time of the flush. Nothing bypasses the throttle; an upstream that was
never sent is simply due immediately.

**Flush of one upstream** produces at most one response:

| Pending | Upstream | Sent |
|---------|----------|------|
| Full | not announced, or index differs | Full; the record takes the new index and the upstream is announced |
| Changes | announced | Delta with the merged changes |
| Removal | announced | Unavailable; the record is deleted |
| Removal | not announced | nothing; the record is deleted |

**Last head sent.** The record's last head is written in one place, the flush, whenever the
response it sends contains a head event (a Full, a Delta or a resync). It is read in one place,
the resync, which compares it with the snapshot's head using `Block.Equals`. A resync never
sends directly: it fills pending and goes through the same flush and the same throttle, so the
comparison is always against what was really sent. A head never goes backwards, because the
snapshot is at least as new as anything sent before and older queued events are ignored.

A removal followed in the same window by a new instance's events: the new instance's index
differs, so the stream reads it. If the view has a head, its Full replaces the pending removal
and the Unavailable is skipped. If not, the pending removal stays, Unavailable goes out, and
the new instance is announced later like any upstream without a head.

### 5. Selector (`internal/protocol`, `internal/upstreams/flow`, `internal/server/emerald`)

- `protocol.RequestUpstreamSelector{Id, Index string}` with a `Key()` (subscriptions that differ
  in it do not share an upstream subscription). It is not added to `labelClassKey`, so the cache
  key is unchanged.
- `mapDshackleSelector` maps `upstream_selector` onto it. An empty id or an empty index maps to
  `RequestUnsupportedSelector` with a reason, which fails the request.
- A matcher admits an upstream when its id equals `Id` and its state's `UpstreamIndex` equals
  `Index`. A miss uses a fixed message that does not list ids.
- The selector may sit at the top level or under AND, OR and NOT like any other selector; no
  special validation.
- `createStrategy` skips `LabelGroupStrategy` for a request that carries the selector at the top
  level or inside top-level AND groups; the chain's `balancing-strategy` orders the one admitted
  upstream. This extends the check the routing headers introduced.
- The existing `RequestUpstreamIdSelector` of the routing headers is not changed.

Because it is a matcher, the rest follows from code that exists: retries and hedges find no
other upstream and the upstream's own error is the response; dispatch fan-out picks through the
same strategy; integrity re-checks have nobody else to ask; sticky and quorum requests add their
own matchers on top; local `newHeads` and `logs` sources filter by the request's selectors.

## Edge cases

| Case | Result |
|------|--------|
| Old event still queued after a snapshot | Ignored by the revision rule; the head never goes backwards |
| The read for a first Full is newer than the event that triggered it | Events for that upstream still queued are at or below the read's revision and are ignored |
| Event dropped by a full subscription buffer | The affected kind is stale until the next resync, at most a minute |
| Head event skipped by the revision rule, or dropped | The next resync sees a head that differs from the last one sent and includes it |
| Head unchanged at a resync | The resync carries no head |
| Removal event dropped | The next snapshot lacks an announced upstream; Unavailable is sent then |
| Upstream fails settings validation, later recovers | Unavailable, then a Full |
| Upstream replaced under the same id | Full with the new index (see section 2) |
| Upstream replaced and the removal event was dropped | The new instance's events carry a different index, so the stream reads it and sends a Full |
| Stream opened before an upstream has a head | The upstream is announced on the first event after its head arrived, from a read |
| Selector names a replaced instance (old index) | `no available upstreams` |
| Selector sent to a server without this change | The server rejects the unknown selector as unsupported |

## Delivery order

1. `drpcorg/public`, branch `separation_api`: the proto change, regenerated stubs, a tagged
   release.
2. nodecore, independent of the new stubs: the supervisor feed, `RequestUpstreamSelector`, the
   matcher and the `createStrategy` check.
3. nodecore, after the tag: the `go.mod` bump, the stream handler, the selector mapping, docs.

## Documentation

`docs/nodecore/12-grpc-server.md`: the new RPC (what each response kind means, the throttle,
the resync, removal) and the selector. The client is described in protocol terms only.

## Testing

- Supervisor feed: changes per event kind, nothing published when nothing changed, a new
  upstream yields every kind, removal, an event carries the revision of its store, a snapshot's
  and a one-upstream read's revision match their content under concurrent events (race
  detector).
- Stream: Full only after a head; delta content; a resync without a head when the head is
  unchanged and with it when it differs from the last head sent; a stale
  queued event after a snapshot is ignored; a first Full is built from a one-upstream read and
  events at or below that read's revision are ignored; an event with a different index leads to
  a Full; removal sent once; lost removal repaired by the
  resync; replacement under the same id; throttle (immediate first send, merge inside the
  window, spacing between sends, interval 0); a chain supervisor added later is followed.
- Selector: mapping, empty fields rejected, matcher on id and index, behaviour under AND/OR/NOT,
  label-balancing skipped, no retry or hedge to another upstream, cache key unchanged,
  subscription key differs.
- The stream is tested against a small fake of `UpstreamStateFeed`; a chain supervisor that
  does not implement the feed is skipped. The existing `ChainSupervisor` test stubs do not
  change.

## Left to the builder

- Names of the new file, the helper and the field group in the supervisor.
- How the comparison skips kinds that did not change. Parts of an upstream state are replaced
  rather than modified in place, so an unchanged part keeps its pointer; verify that for each
  part and compare by value where it does not hold.
- The size of the per-chain subscription buffer.
- How the resync interval is injected for tests (the legacy stream has a `WithResync` variant).
- The exact text of the matcher's miss message.
