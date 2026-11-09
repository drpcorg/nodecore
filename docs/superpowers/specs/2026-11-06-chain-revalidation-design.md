# Chain revalidation over every connector and on suspicious heads

**Date:** 2026-11-06
**Branch:** `ws_check`
**Status:** design agreed in discussion, ready for an implementation plan

## Problem

An EVM upstream can be configured with several URLs (`json-rpc` plus `websocket`). nodecore
validates the chain id only over the internal request connector, which is the `json-rpc` one
when present. Nothing ever asks the websocket endpoint which chain it serves, yet it carries
every client subscription and, in strict mode, the head. A websocket URL that points at the
wrong chain goes unnoticed for the life of the process.

A second, related hole: a head block is published whatever its height. A node that silently
switches chains, or an endpoint that starts answering for another network, feeds heads tens of
thousands of blocks away from the last one straight into the chain state.

The settings validation pipeline already exists, already removes and re-admits upstreams, and
already runs at startup, so the connector check belongs there rather than in the head. The
head keeps only what must be decided per block: whether a suspicious head may be published.

## Goals

1. A chain mismatch on **any** JSON-RPC-speaking connector of an EVM upstream (`json-rpc`,
   `websocket`) refuses the upstream at startup and removes it at runtime, through the existing
   settings validation pipeline.
2. A head block whose height jumps by more than 10000 from the last published head is checked
   against the chain before it is published: a wrong-chain head is dropped and removes the
   upstream, an inconclusive check drops the head only.
3. An upstream removed by the head guard is re-admitted by the settings pipeline as soon as all
   its validators pass, without any new coordination between the two.

## Design

### 1. Chain validation over every JSON-RPC-speaking connector

`EvmChainSpecificObject.SettingsValidators()` today appends one `EthChainValidator` over
`e.connector`. It instead appends one per connector in `e.allConnectors` whose type is
`json-rpc` or `websocket`. The internal connector is in that slice, so the single append goes
away rather than being kept alongside the loop.

The predicate is shared with `detectableConnectorTypes`, which today has the same comparison
inline:

```go
// speaksJsonRpc reports whether a connector carries JSON-RPC calls: the methods
// detectors and the chain validator both reason only over these.
func speaksJsonRpc(connectorType specs.ApiConnectorType) bool
```

`detectableConnectorTypes` filters with it. Its "Websocket counts" comment moves to the
predicate.

The settings processor reduces its validators with `lo.Max`, so one Fatal connector makes the
upstream Fatal, and the existing startup check (`GenericUpstream.Start` → `ValidateSettings`)
refuses a mixed-chain upstream before it is admitted.

`EthChainValidator`'s mismatch log names the upstream, expected and actual chain ids. It must
also name the connector type (`c.connector.GetType()`), otherwise an operator with two URLs
cannot tell which one is wrong.

Every connector gets the same strict validator: no answer is `SettingsError`, a mismatch is
`FatalSettingError`. There is no lenient flavour for secondary connectors (decided 2026-11-06).

**Startup race, not handled on purpose.** `connector.Start()` dials the websocket
asynchronously and the startup validation runs right after it. The websocket processor queues
writes issued before the socket is up and sends them once it connects, so the validator's
request rides through the dial as long as the dial finishes within `internal-timeout` (default
5s), which a healthy endpoint does. A slower dial makes the validator return `SettingsError`
and the upstream is held as "not admitted yet" until the next tick (`validation-interval`,
default 30s). The same race already exists today for
websocket-only upstreams and is accepted the same way.

**Accepted cost:** a websocket that is down for longer than `internal-timeout` yields
`SettingsError`, which keeps an upstream out only while it is not yet in: at startup, or after a
Fatal while waiting to be re-admitted. At runtime it is harmless: `SettingsError` emits nothing
and a valid upstream stays valid. Per-connector health is a separate topic (see Follow-ups).

### 2. Suspicious-head guard

#### Validator source

`chains_specific.ChainSpecific` gets one method (the head processor receives the validator as a
constructor argument from `CreateHeadProcessor`, so `blocks.BlockChainSpecific` stays as it is):

```go
// ChainValidator returns the chain-identity validator bound to this specific's
// connector, or nil when the family has none or chain validation is disabled. The
// head processor uses it to vet a head whose height jumped (see headJumpThreshold).
ChainValidator() validations.SettingsValidator
```

The EVM specific returns `eth_validations.NewEthChainValidator(e.upstreamId, e.connector,
e.chain, e.options)` when `DisableChainValidation` is false, nil otherwise. Every other
specific returns nil. This follows the existing `MethodsProcessor()` convention (nil when the
family has nothing to offer).

The head processor is built from the head specific (`upstreamSpecifics.head`), which is bound
to the head connector. So the validator the guard gets talks to the connector the suspicious
block came over: websocket for a subscription head, json-rpc for a polled one. When head and
internal connector coincide the two specifics are the same object.

#### The guard

In `GenericHeadProcessor`:

- `const headJumpThreshold = 10000` in package `blocks`.
- The processor keeps the last block **it published** in this run in a new field
  (`lastPublished`), cleared on every `Start`. The head's own `GetCurrentBlock()` is no longer
  the reference, because the head stores a block before sending it and a dropped block would
  sit there. The manual-head comparison (`manualBlock.Height > …`) switches to the same
  reference. `Head.GetCurrentBlock()` has no other reader.
- On the `HeadsChan` branch of the drain loop, when a chain validator is present,
  `lastPublished` is set and `|block.Height − lastPublished.Height| > headJumpThreshold`, the
  processor runs `Validate()` and waits for whichever comes first, the verdict or the run
  context ending:
  - `Valid` → log at info, publish the block as usual.
  - `SettingsError` → log at warn, drop the block, keep draining. `lastPublished` does not move,
    so the next block is checked again.
  - `FatalSettingError` → log at error, drop the block, publish `HeadFatalEvent{}` on the
    existing event stream.
  - run context ended → drop the block, discard the verdict when it arrives. The validator call
    finishes on its own within its own `internal-timeout` context.
- The silence timer is reset on a dropped block too: the node is alive, it is just wrong.
- `RpcHead.Stop` and `SubscriptionHead.Stop` clear their stored block, so a block the guard
  dropped (or any stale one) never shows up as the current block of the next run.
- Manual heads (`UpdateHead`, fed by the chain-level lag observer) are never checked. They come
  from the chain, not from the node.
- The first block of a run is never suspicious (no `lastPublished`). After `Resume` the guard
  therefore accepts the first head unchecked; the settings validators have just confirmed the
  chain at that point.

`HeadFatalEvent` is a new `HeadEvent` kind next to `HeadBlockEvent` and `HeadStateEvent`.
`HeadEventProcessor` maps it to `protocol.FatalErrorUpstreamStateEvent{}` through its emitter.
The other subscriber of the head stream (`caps/ws_head_liveness_detector.go`) switches on the
event type without a default and ignores unknown kinds; it needs no change.

**Accepted cost:** the drain goroutine waits for one `Validate()`, bounded by
`internal-timeout` (the validator creates one context for all its retries). During that time
no head of this upstream is published. `GenericHeadProcessor.Stop` does **not** wait for it:
the drain selects on the run context, so Stop returns as soon as the goroutine sees the
cancel. This matters because `PartialStop` runs on the upstream supervisor's single event loop,
shared by every upstream; a Stop that waited on a validation would stall them all. The doc
comment on `Start`, which promises the drain never blocks in network I/O, is rewritten to
state this.

### 3. Re-admission: the settings processor reports, the upstream decides

Today `GenericSettingsEventProcessor` emits `FatalErrorUpstreamStateEvent` only on a
transition into Fatal from Valid or SettingsError, and `ValidUpstreamStateEvent` only on a
transition into Valid from SettingsError or Fatal, tracked in its own
`currentValidationState`. The receiver, `GenericUpstream.processStateEvents`, keeps its own
`validUpstream` flag and already ignores a Fatal when false and a Valid when true.

With the head guard able to remove an upstream, the two holders diverge: after a head-driven
Fatal the settings processor's state is still Valid, so a later Valid tick is not a transition
and nothing re-admits the upstream, ever.

The fix: the settings processor emits its verdict on every tick, Fatal or Valid, and drops
`currentValidationState`. `SettingsError` stays silent (an upstream that started on
SettingsError must stay out until a Valid arrives, and the upstream has no handler for it).
`UnknownResult` stays a skip. This matches `GenericHealthEventProcessor`, which has always
emitted its status every tick.

Consequences, all verified against the only consumer (`upstream_events.go`):

- Every existing transition behaves identically: one Remove on Valid→Fatal, one Valid event and
  one Resume on Fatal→Valid. Repeats hit `continue` before any log line or supervisor event.
- After a head-driven removal, the first settings tick whose validators all pass re-admits the
  upstream. If the head connector really serves another chain, the validator over that same
  connector (added in §1) says Fatal and the upstream stays out. No flapping: both verdicts come
  from the same validator over the same connector.
- Startup is untouched: `GenericUpstream.Start` reads `ValidateSettings()` directly.
- One extra event per `validation-interval` per upstream on the 1000-deep state channel, which
  already carries a health status event per interval and a head event per block.
- One pinned behaviour flips: a Valid tick after the initial Unknown state now emits Valid
  (dropped by the upstream as already valid). In practice the state is never Unknown by the
  time ticks run, since the startup validation stores a result first.

## Behaviour summary

| Situation | Before | After |
|---|---|---|
| ws URL on another chain, http right, at startup | admitted | refused (`invalid upstream settings`) |
| ws URL switches chain at runtime | unnoticed | removed on the next settings tick, or at once if the head jumps |
| ws endpoint down longer than `internal-timeout`, upstream already valid | upstream serves HTTP, no subscriptions | unchanged (SettingsError emits nothing) |
| ws endpoint down at startup or while removed | admitted / re-admitted on the json-rpc verdict | held out (SettingsError) until the ws answers |
| head jumps > 10000, chain check Valid | published | published |
| head jumps > 10000, chain check inconclusive | published | dropped, next head rechecked |
| head jumps > 10000, chain check Fatal | published | dropped, upstream removed |
| upstream removed by head guard, connector recovers | n/a | re-admitted on the next Valid settings tick |
| `disable-chain-validation: true` | no chain check | no chain check, no head guard |

## Tests

- `evm_chain_specific`: a json-rpc + websocket upstream yields two chain validators, one per
  connector; a rest-additional connector yields none; `disable-chain-validation` yields none.
- `eth_chain_validator`: the mismatch log carries the connector type (existing mock-connector
  tests extended, no new harness).
- `head_processor`: jump on the right chain is published; jump with an inconclusive check is
  dropped and the following head is rechecked; jump on the wrong chain is dropped and
  `HeadFatalEvent` is published; no validator means no check; manual heads bypass the guard;
  the first head of a run is not checked; Stop during a validation returns without waiting for
  the verdict and the block is not published; `lastPublished` is cleared on Start.
- `head` (`RpcHead`, `SubscriptionHead`): Stop clears the stored block.
- `block_event_processor` (`HeadEventProcessor`): `HeadFatalEvent` becomes
  `FatalErrorUpstreamStateEvent`.
- `validation_event_processor`: Valid and Fatal are emitted on every tick; SettingsError and
  Unknown emit nothing. The two transition tests are rewritten accordingly.
- `upstream` level: head-driven Fatal removes the upstream; a later Valid settings tick
  re-admits it (the existing hand-emitted event tests already cover the dedupe).

## Docs

`docs/nodecore/05-upstream-config.md`, validators table, chain-id row: the check runs over every
json-rpc and websocket connector of the upstream, a wrong-chain websocket removes the upstream,
and a head whose height jumps by more than 10000 is checked against the chain before it is
accepted.

## Follow-ups (out of scope, recorded 2026-11-06)

- **Cosmos-EVM upstreams whose internal connector is not EVM** (`tendermint`/`rest`/`grpc` plus
  `websocket`). The settings processor is fed by the probe specific, which is the Tendermint or
  Cosmos one there, so the websocket gets no chain validator from §1. The head guard still
  removes such an upstream on a wrong-chain jump, but the next all-Cosmos settings tick
  re-admits it and the first head of the new run is unchecked. Net effect: no protection for
  that config, same as today. Fix when wanted: the three Cosmos specifics run the same
  `speaksJsonRpc` loop over their `allConnectors`.
- **Per-connector health.** A multi-API upstream has one availability status, derived from
  validators over the internal connector; a dead secondary connector is only reflected through
  the cap pipeline (websocket caps). Per-connector status, connector-aware routing and
  per-connector health probes are a design of their own. This task is identity only.

## Left to the builder

- Exact log wording and levels beyond what is stated above.
- Test fixtures for a head processor with a validator (a stub `SettingsValidator` returning a
  scripted sequence is enough).

## References

- `internal/upstreams/chains_specific/evm_specific/evm_chain_specific.go`
  (`SettingsValidators`, `detectableConnectorTypes`),
  `internal/upstreams/validations/eth_validations/eth_chain_validator.go`,
  `internal/upstreams/blocks/head_processor.go`,
  `internal/upstreams/event_processors/block_event_processor.go` (`HeadEventProcessor`),
  `internal/upstreams/event_processors/validation_event_processor.go`,
  `internal/upstreams/upstream_events.go`, `internal/upstreams/upstream_factory.go`
  (`getUpstreamSpecifics`).
