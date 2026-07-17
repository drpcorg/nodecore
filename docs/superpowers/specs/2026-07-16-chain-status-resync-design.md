# Self-healing chain-status stream — design

- **Date:** 2026-07-16
- **Status:** Implemented — branch `fix/chain-status-resync`
- **Area:** `internal/server/emerald` (`sub_chain_status.go`), `internal/upstreams` (`chain_supervisor.go`, `upstream_events.go`), `internal/protocol` (`data.go`)

## 1. Problem

The `SubscribeChainStatus` gRPC stream is **delta-based**: one `FullResponse`
per chain at subscription time, then only change events. Delta delivery is not
reliable end-to-end, and there is no reconciliation mechanism — so a single
lost delta desynchronizes the consumer **forever**.

Production incident that surfaced it: a settings validator
(`eth_log_index_validator`) stopped an upstream that served corrupt `logIndex`
data (upstream-node bug, erigontech/erigon#22504). After the node was fixed,
nodecore re-validated the upstream and logged `statuses=[AVAILABLE/1]`
continuously — while a stream consumer kept reporting the chain **Unavailable
for 75 more minutes**, until its connection was rebuilt. A second episode the
next morning showed per-connection divergence across identical subscribers of
the same instance (most stale, a few healthy) — direct proof of
per-connection delta loss rather than a state bug on either end.

Two independent defects compound:

1. **Lossy fan-out under a delta-only protocol.** The subscription manager
   (`pkg/utils/subscriptions.go` `Publish`) intentionally drops an event when
   a subscriber channel is full — back-pressure protection; a busy instance
   logs hundreds of such drops per day. Consumers have equivalent lossy hops
   on their side. One dropped `Status: Available` wrapper = permanently stale
   subscriber.

2. **Chain supervisor ignores `ValidUpstreamEvent`.**
   `RemoveUpstreamEvent` (emitted on `FatalSettingError`) deletes the
   upstream from the chain map, but the recovery event has no handler in
   `processEvents`. The upstream is re-added only when a later
   `StateUpstreamEvent` happens to fire — and those are suppressed by a
   `Same()` check unless some sub-state actually changed. A node that comes
   back identical to how it left is never re-added. (In both production
   episodes re-add worked only because restarting the upstream node changed
   its lower bounds.)

## 2. Goal

A consumer of `SubscribeChainStatus` converges to nodecore's actual chain
state within a **bounded time** (one resync interval), no matter what happens
to individual deltas on either side of the stream; and a recovered upstream
re-enters its chain **deterministically**, not as a side effect of incidental
state churn.

### Non-goals

- **No lossless fan-out.** The `Publish` drop is deliberate back-pressure;
  with resync in place, per-event reliability is unnecessary. Rejected also
  because it touches the hot path of every subscription in the process.
- **No consumer-side changes.** The producer-side resync repairs every
  consumer at once; requiring consumer deploys was rejected.
- **No resync-interval configuration.** A constant is enough until proven
  otherwise.
- No changes to the initial-handshake semantics (`FullResponse`, wait-for-head).

## 3. Key decisions (settled → implemented)

| # | Decision | Choice |
|---|---|---|
| 1 | Repair mechanism | Periodic state snapshot from the producer, not reliability per event |
| 2 | Snapshot framing | Regular delta-style response, `FullResponse` **not** set — consumers use that flag only to (re)create per-chain objects on reconnect |
| 3 | Snapshot content | All state wrappers: status, methods, lower bounds, finalization blocks, sub-methods, labels — **deliberately no head** (§6) |
| 4 | Interval | `defaultChainStateResyncInterval = 1 minute`, per chain, per connection |
| 5 | Before first full | No snapshots (`fullSent` gate): consumers create their per-chain object only from a full response and skip earlier state updates |
| 6 | Recovery re-add | `ValidUpstreamEvent` carries the upstream-state snapshot; chain supervisor applies it symmetrically to `RemoveUpstreamEvent` |
| 7 | Head on re-add | Re-registered in fork choice only when the snapshot head is non-empty, so a stale snapshot cannot zero the chain head |
| 8 | Nil-state `ValidUpstreamEvent` | Ignored (defensive) |

## 4. Terminology

- **Delta** — a `SubscribeChainStatusResponse` carrying only changed state
  wrappers, produced by `ChainSupervisorState.Compare` after `updateState()`.
- **Wrapper** — one typed state fragment (`StatusWrapper`, `MethodsWrapper`,
  `LowerBoundsWrapper`, `BlocksWrapper`, `LabelsWrapper`, `SubMethodsWrapper`,
  `HeadWrapper`) mapped 1:1 to a `ChainEvent` on the wire.
- **Snapshot** — the periodic resync response: the full current state as a
  wrapper list, minus the head (§6).
- **Settings validators** — per-upstream data-quality checks (chain id, log
  index, call limit, …); `FatalSettingError` → `RemoveUpstreamEvent`,
  recovery → `ValidUpstreamEvent`.

## 5. Architecture

```
 settings validator ──► upstream event loop ──► chain supervisor ──► per-connection
 (Fatal/Valid)          (upstream_events.go)    (chain_supervisor.go)  subscription      ⚠ lossy
                                                    │ updateState()    (subscriptions.go)
                                                    ▼                        │
                                              Compare → deltas               ▼
                                                                   per-chain goroutine
                                                                   (sub_chain_status.go)
                                                                     │  deltas as-is
                                                                     │  + snapshot every 60s   ◄── NEW
                                                                     ▼
                                                              responses chan → gRPC Send ──► consumer
                                                              (blocking, not lossy)          (own lossy hops ⚠)
```

Any `⚠` hop may drop a delta; the snapshot repairs the consumer within one
interval regardless of where the loss happened.

## 6. Periodic resync (`sub_chain_status.go`)

Each per-chain goroutine gets a `time.Ticker`. On tick (only after the
initial full response went out):

```go
state = chainSupervisor.GetChainState()
sendResponse(ctx, responses, stateWrappersToResponse(grpcId, snapshotStateWrappers(state)))
```

`snapshotStateWrappers` rebuilds the wrapper list from the current state —
status, methods, lower bounds, blocks, sub-methods, labels — **without the
head**, for two reasons:

- head freshness is already guaranteed by the per-block head events;
- known consumers reduce any response that carries a head to a *head-only*
  update for an already-known chain — a snapshot with a head would lose
  exactly the state it is meant to repair. A head-less response routes into
  their state-update path, which provably works.

Cost: one small message per chain per connection per minute.

## 7. Deterministic re-add (`chain_supervisor.go`, `upstream_events.go`, `protocol/data.go`)

`ValidUpstreamEvent` gains a `State *UpstreamState` field, filled by the
upstream event loop from the snapshot it already holds. The chain supervisor
handles the event symmetrically to removal:

```go
case *protocol.ValidUpstreamEvent:
    if eventType.State != nil {
        availabilityMetric.Set(...)
        b.upstreamStates.Store(event.Id, eventType.State)
        b.updateState()                       // recomputes + publishes the delta
        if !eventType.State.HeadData.IsEmptyByHeight() {
            b.updateHead(...)                 // re-register in fork choice
        }
    }
```

## 8. Edge cases

- **Consumer without the per-chain object on a live stream** (never got the
  initial full, e.g. chain had no head at subscribe time): it skips state-only
  responses; unchanged behavior — the per-chain goroutine still sends the
  full response first once a head appears, snapshots only after.
- **Chain with zero upstreams:** snapshot truthfully carries
  `Status: Unavailable`, empty methods/bounds.
- **`ValidUpstreamEvent` with empty head in the snapshot:** state re-added,
  fork-choice head left to the upstream's next head event (decision #7).
- **Repeated remove/re-add flapping:** each cycle publishes its deltas as
  before; snapshots bound the damage of any lost one.

## 9. Testing

- `TestChainSupervisorValidUpstreamEventRestoresRemovedUpstream` — remove →
  valid restores the upstream state, chain status, head, and emits a
  `StatusWrapper(Available)` delta to subscribers.
- `TestChainSupervisorValidUpstreamEventWithoutStateIsIgnored` — defensive
  nil-state case.
- `TestSubscribeChainStatus_PeriodicResyncResendsStateWithoutHead` — simulates
  the lost delta (state mutated with no event reaching the stream) and
  verifies the snapshot repairs it: non-full response, carries status and
  methods, no head event present.
- `TestSubscribeChainStatus_NoResyncBeforeFirstFullResponse` — no snapshots
  before the initial full response.

## 10. Live validation

A/B harness: mock EVM upstream with toggleable logIndex corruption
(`first log index is 200 instead of 0` — the exact signature of
erigontech/erigon#22504) + a gRPC client on `SubscribeChainStatus`, strict
mode, `validation-interval: 1s`.

```
09:37:13  STATUS=AVAIL_UNAVAILABLE   <- validator stops the upstream
09:37:33  STATUS=AVAIL_OK            <- recovery propagates
09:37:59  STATUS=AVAIL_OK methods=61 bounds=0 finalization subs=0 nodes=1   <- snapshot
09:38:59  STATUS=AVAIL_OK methods=61 bounds=0 finalization subs=0 nodes=1   <- snapshot, 60s later
```

Snapshots carry no head; heads keep flowing as separate per-block events.
On the pre-fix binary the same scenario recovers only when incidental state
churn (restarted detectors) happens to fire a `StateUpstreamEvent`, and a
lost delta is never repaired.

## 11. Key code references

- `internal/server/emerald/sub_chain_status.go` — `SubscribeChainStatusWithResync`,
  `snapshotStateWrappers`, the `fullSent` gate.
- `internal/upstreams/chain_supervisor.go` — `processEvents` (`ValidUpstreamEvent`
  case), `updateState`, `updateHead`.
- `internal/upstreams/upstream_events.go` — `processStateEvents`
  (`ValidUpstreamStateEvent` → `ValidUpstreamEvent{State}`).
- `pkg/utils/subscriptions.go` — `Publish` (the deliberate drop this design
  routes around).

## 12. Open questions / future

- Consumers that reduce a head-carrying response to a head-only update would
  still lose state if a head ever gets batched together with state wrappers;
  worth fixing on the consumer side eventually, though this design never
  produces such a batch.
- If minute-scale staleness ever matters, the interval can become a server
  config knob; not needed today.
- The same resync pattern could cover `NativeSubscribe`-style streams if they
  grow state semantics.
