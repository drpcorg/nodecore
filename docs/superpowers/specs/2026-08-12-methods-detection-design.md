# Upstream method detection

## Goal

In strict (dshackle) mode, upstreams are generic self-hosted nodes rather than third-party providers, and nodecore currently assumes every node serves every method in its chain's spec. That is wrong in both of the ways operators actually hit:

- a geth built without `--http.api=trace` still advertises every `trace_*` method in the eth spec, because the spec is the only source of truth;
- a node that *could* serve `debug_*`/`trace_*` but was started without the flag is indistinguishable from one that can.

Dshackle solves this with `UpstreamRpcMethodsDetector` (`src/main/kotlin/io/emeraldpay/dshackle/upstream/UpstreamRpcMethodsDetector.kt`) and two implementations, for eth and polkadot. This spec introduces the equivalent for nodecore: a per-chain method-detection pipeline built like the existing `caps` / `labels` / `lower_bounds` pipelines, which narrows an upstream's method set to what the node actually serves.

Scope of this spec is **EVM only**. Polkadot's `rpc_methods` fits the same interface and is deliberately left for separate work.

## Semantics

**Detection only subtracts.** The chain's method spec is the ceiling. Detection removes methods from it and never adds any. Consequences:

- The spec's job becomes "everything this chain family could possibly expose", and the detector's job is "what this particular node actually exposes". Those are cleanly separable: the spec stays a static, reviewable artifact, and per-node truth is discovered at runtime instead of hand-maintained as config.
- Adding a method to a spec becomes low-risk in strict mode, which is the opposite of today, where every spec addition silently widens what every generic node claims to serve.
- No method is ever served without a spec entry behind it, so cache policy, `tag-parser`/`modify-parser` rewriting and sticky/integrity flags always apply. A node self-reporting a method nodecore has no spec for does not get it enabled.

This is a deliberate divergence from dshackle, whose polkadot detector enables everything `rpc_methods` returns, including methods absent from its own method list.

**Default mode keeps its current behaviour.** Detection defaults off outside strict mode; the reactive `MethodBanHook` (`internal/upstreams/flow/method_hook.go`) remains the safety net there. Default mode learns from failures, strict mode asks up front. Both keep the ban path — they compose (see [Composition](#composition)).

The accepted corollary: because the spec now aims to list *every* method a chain family could expose, a default-mode upstream advertises more methods a given provider may not serve, and leans harder on the ban path to discover that — one failed request per method per upstream per `ban-duration`. That is deliberate. Method-availability probes exist precisely for methods a node might or might not have, so any spec entry for one has this property in default mode; the alternative would be keeping the spec narrow, which is the over-restriction this design rejects. Specs are lists of all methods.

**Config wins over detection.** `methods.enable` is an explicit operator override and is applied last. This matches dshackle, and matches what the ban path already does: `upstream_events.go:49` refuses to ban anything listed in `EnableMethods`.

**Detection is asynchronous and does not gate the upstream's start.** An upstream begins serving with the full spec set and narrows when the first detection round lands, a fraction of a second later. Over-enablement during that window is the status quo, and the ban hook covers it — trading a brief window for a blocking network call in `Start()` is not worth it. See [Detection lifecycle](#detection-lifecycle).

## Config

One new field on `chains.Options` (`pkg/chains/options.go`), resolvable at the three usual levels — global chain settings, chain defaults, per-upstream:

```go
DisableMethodsDetection *bool `yaml:"disable-methods-detection"`
```

```yaml
upstreams:
  - id: geth-full
    chain: ethereum
    options:
      disable-methods-detection: false
```

Default resolution in `setOptionsDefaults` (`internal/config/option_defaults.go`), following `DisableLowerBoundsDetection` and `DisableLabelsDetection` exactly:

```go
if upstreamOptions.DisableMethodsDetection == nil {
    upstreamOptions.DisableMethodsDetection = resolveBool(
        getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableMethodsDetection }),
        getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableMethodsDetection }),
        lo.Ternary(upstreamMode == StrictMode, false, true),
    )
}
```

So: on in strict mode, off in default mode, overridable at any level. It does **not** consult `DisableValidation` — that flag gates only the settings and health validators (`validation_event_processor.go:96, 176`); `labels` and `lower_bounds` do not respect it either.

`MethodsConfig` is unchanged. `enable`/`disable` keep their current meaning; the conflict between config and detection is resolved by composition order, not by new config.

## Package layout

Mirrors `caps` / `labels` / `lower_bounds`:

```
internal/upstreams/methods/
  methods.go                    (existing)
  detector.go                   MethodsDetector, DetectableMethods
  methods_processor.go          MethodsProcessor, GenericMethodsProcessor
  evm_methods/
    rpc_modules_detector.go     RpcModulesDetector
    method_probe_detector.go    MethodProbeDetector
```

No import cycle: `methods` gains dependencies on `connectors`, `protocol` and `pkg/methods`, none of which import `internal/upstreams/methods`.

### The detector interface

```go
type MethodsDetector interface {
    DetectUnsupported(ctx context.Context) mapset.Set[string]
}
```

The return value is **three-valued**, and that distinction carries the design:

| value | meaning |
|---|---|
| non-empty set | "these methods are missing" |
| empty, non-nil | "I asked, and nothing is missing" |
| `nil` | "I have never managed to find out" |

Collapsing the last two would make a briefly unreachable node indistinguishable from a fully-featured one: an empty verdict published during an outage restores every method the previous round stripped, and stands until the next round. `GenericMethodsProcessor` keeps each detector's last non-nil verdict, so a detector returning `nil` contributes what it last established instead of dropping out of the merge.

A detector that can answer for only part of its subject retains the rest itself, at whatever granularity it owns - see `MethodProbeDetector`, which retains per probe so one timed-out call does not discard what is known about the other seven.

There is deliberately no `Domain()`, unlike `caps.CapDetector`. Cap detectors make *positive* assertions, so the processor must know which slice of the merged set each detector owns in order to replace just that slice. Unsupported-method sets are negative and additive, so a plain union is unambiguous and there is nothing to attribute.

There is no `DetectorInput` type, unlike `caps.DetectorInput`. Cap detectors need the ws connector, the head processor and the method set — things only the upstream level knows — so they must be passed down. A method detector needs the internal-request connector, `InternalTimeout`, the chain and the upstream id, and `EvmChainSpecificObject` already holds every one of them (`evm_chain_specific.go:28-37`, populated by `NewEvmChainSpecific` from `upstreamConnectorsInfo.internalRequestConnector` at `upstream_factory.go:216-224`). So detection follows `LabelsProcessor()`, not `CapDetectors(input)`, and `ChainSpecific` gains:

```go
// MethodsProcessor returns the chain's method-detection processor. Detection
// subtracts from the spec-derived method set; chains with no way to introspect a
// node return nil and keep the full spec.
MethodsProcessor() methods.MethodsProcessor
```

Implemented in `evm_specific`; the other thirteen implementations return nil, so their behaviour is unchanged.

`evm_specific.MethodsProcessor()` builds the detectors from its own fields, mirroring `LabelsProcessor()` at `evm_chain_specific.go:52-54`, and computes the base set itself:

```go
func (e *EvmChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
    base := methods.DetectableMethods(e.chain.MethodSpec, connectorTypes(e.allConnectors))
    return methods.NewGenericMethodsProcessor(
        e.ctx,
        e.upstreamId,
        []methods.MethodsDetector{
            evm_methods.NewRpcModulesDetector(e.upstreamId, e.chain.Chain, e.connector, e.options.InternalTimeout, base),
            evm_methods.NewMethodProbeDetector(e.upstreamId, e.chain.Chain, e.connector, e.options.InternalTimeout, base),
        },
        methods.DetectionInterval,
    )
}
```

The base set is the **spec** set, not `creationData.upstreamMethods` — it is not pre-filtered by `methods.enable`/`disable`. Detection and config are independent inputs, combined once in [Composition](#composition); pre-filtering would apply config twice and would let a config-enabled method that is absent from the spec reach a detector.

`e.connector` is `connectorsInfo.internalRequestConnector` (`upstream_factory.go:342, 357`), resolved from `conf.GetBestConnector(config.DefaultMode)` — the connector every other internal request already uses, passed to all thirteen chain-specific constructions in `getChainSpecific` (`upstream_factory.go:208-316`). No fallback logic and no new connector selection.

### The shared domain helper

```go
// DetectableMethods is the base a detector may form opinions about: the chain's
// spec methods minus locally-served ones, whose support does not depend on the node.
func DetectableMethods(specName string, connectorTypes []specs.ApiConnectorType) mapset.Set[string]
```

The `IsLocal()` filter lives here so no detector can forget it. Three eth spec methods are `"local": true` — `eth_unsubscribe` (`pkg/methods/specs/eth-json-rpc.json:387`), `net_version` (:434), `eth_chainId` (:477) — and `net_version`'s module is `net`, which a node reporting only `{"eth","debug","web3"}` does not list. Without the filter it would be stripped.

Serving would not break, because `createRequestProcessor` routes on `IsLocal()` *before* any upstream matching (`execution_flow.go:333`) and `LocalRequestProcessor` answers from `chains.ConfiguredChain`. The damage is elsewhere: `ChainSupervisor.GetSupportedMethods()` (`chain_supervisor.go:130`) is the union across upstreams and feeds the emerald chain-status API (`internal/server/emerald/sub_chain_status.go:181, 225`), which dshackle-mode clients read to decide what they can route here. Stripping a local method makes a client stop sending nodecore something nodecore answers locally and infallibly — the same class of error as over-enablement, in the other direction.

## The EVM detectors

`rpc_modules` is a **reliable negative and an unreliable positive**:

- module `trace` absent ⇒ `trace_callMany` is definitely absent;
- module `trace` present ⇒ `trace_callMany` is *probably* present, but a given build may lack it. This is the entire reason a probe list exists.

The two detectors are therefore **peers, not stages**: `RpcModulesDetector` answers for every base method by module membership, `MethodProbeDetector` answers for the eight methods module membership cannot settle, and the processor unions their verdicts. A method is stripped if its module is absent **or** its probe says not-available.

An earlier draft staged them - probing only the methods that survived module attribution - on the grounds that a probe returning `Unknown` must not resurrect a method whose module is absent. Under union semantics that cannot happen: an inconclusive probe contributes nothing, so the module verdict stands on its own. Ordering bought only a smaller request count, and it cost a bespoke composer (`EvmMethodsDetector`) plus a second place for retention to live. Peers are simpler and equally correct.

The price is paid in requests: on a node without `trace`/`debug`, the probe detector still asks about ~6 methods whose module is already known to be absent, and each answers `-32601`. Those calls go out over the observer-wrapped `internalRequestConnector`, so they are recorded as upstream errors and feed the error-rate rating function. At the hourly [detection interval](#the-processor) that is ~144 synthetic errors per upstream per day, which is small but not zero, and proportionally larger on a low-traffic upstream. It is also not new: every other detector - labels, lower bounds, block probes - uses the same instrumented connector, and the lower-bound binary searches generate failing calls today. Excluding detection traffic from the dimension and stats hooks is the clean fix and is deliberately out of scope here.

When `rpc_modules` is not implemented at all, its detector returns `nil` on every round, contributes nothing, and the probes decide alone - dshackle's `switchIfEmpty` fallback, falling out of the three-valued contract rather than being coded as a special case.

### RpcModulesDetector

Sends `rpc_modules` via `protocol.NewInternalUpstreamJsonRpcRequest` (`internal/protocol/json_rpc_request.go:48`) and parses the `{"eth":"1.0","debug":"1.0"}` reply with sonic. Each base method is attributed to a module by its `prefix_`; unsupported is the set of base methods whose module is absent from the reply.

- Methods with no attributable `prefix_` are left alone.
- On any error, malformed body, empty module map, or a node that does not implement `rpc_modules`, it returns **nil**. It holds no state: the processor keeps its last verdict, so "we could not ask" never degrades into "everything is supported".

### MethodProbeDetector

Calls each of its methods with empty params and classifies the response (see [Error classification](#error-classification)): `MethodNotAvailable` ⇒ strip, `MethodAvailable` ⇒ keep, `Unknown` ⇒ no answer.

Unlike `RpcModulesDetector` this detector **is** stateful, because its subject is divisible: it keeps the last conclusive answer **per probe**. A round where three probes answer and five time out must not report only the three - that would silently restore whatever the five had established. Only conclusive results are merged into that map, so an inconclusive probe leaves the previous answer in place, and the detector returns nil only while no probe has ever answered.

The map has a single writer despite the concurrency: the probe goroutines write to an indexed slice, and the merge into the map happens after their `wg.Wait()`. Rounds themselves never overlap, since the processor's `wg.Wait()` for round N happens-before it spawns round N+1.

The probe list starts as dshackle's (`BasicEthUpstreamRpcMethodsDetector.kt:36-46`), intersected with the chain's base set so a probe naming a method absent from the spec is skipped automatically:

```
eth_getBlockReceipts, trace_callMany, trace_rawTransaction, eth_simulateV1,
eth_getStorageValues, debug_storageRangeAt, eth_getTdByNumber, eth_callBundle
```

It lives as a package-level var in `evm_methods`; `NewMethodProbeDetector` intersects it with the chain's base set at construction.

**Hard rule: read-only methods only.** Nothing in the `eth_sendRawTransaction` family, or anything else that can mutate node or chain state, goes in this list. A detector runs unprompted on every upstream start.

Params are junk/empty, as in dshackle. Getting the params right per method is not worth it: matching the not-available patterns is what matters, and a wrong-params reply is itself positive evidence the method exists.

## The processor

Same shape as `LabelsProcessor` (`internal/upstreams/labels/label_processor.go:13-16`) and `CapProcessor` (`caps/cap_processor.go:16-19`) — a lifecycle plus a subscription:

```go
type MethodsProcessor interface {
    utils.Lifecycle
    Subscribe(name string) *utils.Subscription[mapset.Set[string]]
}
```

```go
func NewGenericMethodsProcessor(
    ctx context.Context,
    upstreamId string,
    detectors []MethodsDetector,
    delay time.Duration,
) *GenericMethodsProcessor
```

`Start()` spawns a goroutine that detects **immediately and then every `delay`**, exactly like `GenericLabelsProcessor.detectLabels` (`label_processor.go:86-104`): one round up front so a fresh upstream narrows within a round trip, then a `time.After(delay)` loop. Nothing blocks the caller.

Each round runs every detector concurrently and unions their verdicts, but the merge is over **`latest`, not over this round's answers**: a detector's slot is replaced only when it returns non-nil. Dropping an unanswering detector's contribution would restore every method it had previously stripped - the same failure the three-valued contract exists to prevent, one level up. An empty non-nil verdict *does* clear a slot, so a node that gains modules converges. `latest` lives in the `Start()` goroutine rather than on the struct: that goroutine is its only owner, and a restarted processor should begin with no history. This mirrors `GenericCapProcessor.aggregate`'s per-detector `latest` slice.

Until some detector has ever answered, the merge is empty for lack of information and must not be published - that would strip nothing while looking like a real verdict. The processor holds instead, and the upstream keeps the full spec it had before detection existed.

The merged set is published **only when it differs from the last published set**, following `GenericCapProcessor.aggregate` (`caps/cap_processor.go:122-126`) rather than `GenericLabelsProcessor`, which republishes every round. Over a value that rarely changes the steady state is "no publish", so the event loop and the chain-supervisor recompute are never woken for nothing. Dedup in the `processStateEvents` switch stays as a second layer, since a restarted processor's first round republishes unconditionally.

`NewGenericMethodsProcessor` returns nil for an empty detector list, so the whole pipeline disappears for non-EVM chains — the same nil-means-absent convention as `NewGenericCapProcessor` and `NewGenericLabelsProcessor`.

`delay` is `methods.DetectionInterval`, a flat **one hour**, rather than a multiple of `ValidationInterval` the way the labels processors derive theirs. Deriving it looks tidier but has a failure mode: an operator setting `validation-interval: 5s` for tight health checks would silently get detection every 100 seconds, multiplying the probe traffic discussed under [The EVM detectors](#the-evm-detectors) by a factor of 36. A constant is predictable, and the value it tracks - which `--http.api` flags a node was started with - changes on the order of months. dshackle detects once at launch and never again, so hourly is already the more responsive end of the trade.

Each call is bounded by `options.InternalTimeout`, detectors run concurrently, and the probes run concurrently within `MethodProbeDetector` — so a round converges in roughly one `InternalTimeout` (5s default) even against an unresponsive node, rather than nine sequential round trips. Dshackle parallelises the same way with `Mono.zip`.

### MethodsEventProcessor

New `MethodsEventProcessorType` in `internal/upstreams/event_processors/event_processor.go`, and a `MethodsEventProcessor` that is a direct analogue of `LabelsEventProcessor` (`event_processors/labels_event_processor.go`): `Start()` starts the processor, subscribes, and forwards published sets to the emitter from a goroutine; `Stop()` stops both.

Wiring mirrors labels exactly: `createMethodsProcessor(chainSpecific, options)` in `upstream_factory.go` returns nil when `*options.DisableMethodsDetection` and otherwise `chainSpecific.MethodsProcessor()` — the same three lines as `createLabelsProcessor` (`upstream_factory.go:184-189`) — and `CreateMethodsEventProcessor` in `upstream_processors.go` returns nil when that processor is nil, like `CreateLabelsEventProcessor`.

## The state event

```go
// UnsupportedMethodsUpstreamStateEvent replaces the upstream's detected-unsupported
// set wholesale. Detection produces a whole-set view - the pipeline has already
// merged every detector's verdict - so this carries the full set rather than
// incremental strip/restore.
type UnsupportedMethodsUpstreamStateEvent struct {
    Methods mapset.Set[string]
}
```

Sibling of `CapsUpstreamStateEvent`, for the reason its comment already gives (`upstream_state_events.go:143-146`): the processor has merged every detector's view, so the event carries the set.

`Same()` returns false; dedup happens in the `processStateEvents` switch by comparing against the tracked set, matching how the ban path dedups at `upstream_events.go:49` rather than through `Same`. The set therefore does not need to live on `UpstreamState`.

### Why not reuse BanMethod/UnbanMethod

- **The ban expires.** `upstream_events.go:52` schedules `time.AfterFunc(u.upConfig.Methods.BanDuration, ...)` to auto-unban, defaulting to 5 minutes (`internal/config/defaults.go:394`). A geth without `--http.api=trace` would have its `trace_*` methods stripped at start and quietly restored five minutes later, forever, on a loop. This alone rules it out.
- **Cardinality.** Detection yields a whole set at once; stripping `trace_*` + `debug_*` + `erigon_*` from the eth spec is on the order of a hundred methods. As ban events that is a hundred trips through `processStateEvents`, each rebuilding the full method map via `newUpstreamMethods` (cloning the spec map, re-running group expansion) and each calling `publishUpstreamEvent` → chain-supervisor state recompute. One event is one rebuild.
- **They must coexist, and unban must not overreach.** A method detection says exists can still be banned when it starts failing. Sharing one set would let a ban timer firing clear a detection verdict.
- **Meaning and logs.** "The method X has been banned on upstream Y" (`upstream_events.go:55`) is wrong for "this node was never built with that module": different cause, different operator action, different log line.

## Composition

`processStateEvents` gains an `unsupportedMethods` set beside `bannedMethods` (`upstream_events.go:15`), and `newUpstreamMethods` takes both:

```go
func (u *GenericUpstream) newUpstreamMethods(banned, unsupported mapset.Set[string]) methods.Methods
```

Order:

```
spec − unsupported − config disable − banned + config enable
```

The existing helper already produces this order, so the change is only to what gets unioned into the disable list:

```go
newConfig := &config.MethodsConfig{
    EnableMethods:  u.upConfig.Methods.EnableMethods,
    DisableMethods: lo.Union(banned.ToSlice(), unsupported.ToSlice(), u.upConfig.Methods.DisableMethods),
}
```

`methods.NewUpstreamMethods` removes disabled entries before adding enabled ones (`methods.go:40-67`), so `config enable` wins by construction. Unsupported entries are always concrete method names, never group names, so they only ever hit the non-group branch of the disable loop.

`config enable` winning is the same precedence the ban path already grants via its `slices.Contains(u.upConfig.Methods.EnableMethods, ...)` guard (`upstream_events.go:49`). Both paths call one shared helper for that check rather than re-deriving it.

Keeping the two sets separate is load-bearing: `UnbanMethodUpstreamStateEvent` removes from `bannedMethods` only, so a 5-minute ban timer firing can never resurrect a method the node structurally lacks.

A warning is logged per method that `config enable` forces back on against detection's verdict. That is the operator's most likely misconfiguration and is otherwise invisible.

## Detection lifecycle

`MethodsEventProcessor` is registered in `Resume()` / `PartialStop()` (`upstream.go:257-273`) exactly like its siblings. Two things drive a detection round:

- **The ticker**, which is the primary mechanism: immediately on processor start, then every `methods.DetectionInterval` (one hour). At boot the upstream serves the full spec set until that first round lands and `processStateEvents` applies the event — one round trip in the normal case.
- **Processor restart** via `Resume()`, which re-runs the immediate round.

The ticker is load-bearing rather than a belt-and-braces addition, because `Resume()` alone would miss the case the whole feature exists for. `Resume()` fires only on `ValidUpstreamEvent` (`upstream_supervisor.go:188`), which comes from **settings** validation going invalid→valid — chain-id and friends. An operator adding `trace` to `--http.api` and restarting geth keeps the same chain-id, so settings validation never fails; the restart surfaces as an availability change through `StatusUpstreamStateEvent`, which does not touch `PartialStop`/`Resume`. Without a ticker, a widened node would keep its old narrowed verdict until nodecore itself restarted.

Detection failure is non-fatal: log a warning, publish nothing, and let the next round correct it. The upstream keeps the full spec (over-permissive, with the ban hook covering it) rather than being held back or torn down over a probe that timed out. `rpc_modules` not being implemented is not a failure at all — that detector simply contributes nothing and the probes still run.

A failed round at boot therefore leaves the upstream un-narrowed for up to an hour. That is accepted: it is the behaviour nodecore had before detection existed, the ban hook still covers the methods that actually get called, and dshackle - which detects once at launch and never retries - would leave it that way permanently.

Nothing in `Start()` blocks on detection. Resolving methods before first traffic would mean a synchronous network round in `Start()`, and the window it closes is a fraction of a second of the over-enablement that is already today's steady-state behaviour — not worth paying for.

## Error classification

`methodPatterns` moves out of `internal/upstreams/flow/method_hook.go:12-18` into `internal/protocol`, next to `errors.go`, as a tri-state classifier:

```go
type MethodAvailability int

const (
    MethodAvailabilityUnknown MethodAvailability = iota
    MethodNotAvailable
    MethodAvailable
)

// ClassifyMethodAvailability decides what an upstream error says about whether a
// method exists. Not-available patterns are checked first: an unavailable method is
// the answer that matters, and a wrong-params reply only ever means "it exists".
func ClassifyMethodAvailability(err *ResponseError) MethodAvailability
```

- `MethodNotAvailable`: the existing patterns from `method_hook.go`, plus code `-32601` (`NoSupportedMethod`, already in `errors.go:19`).
- `MethodAvailable`: dshackle's `availableRegexps` — `missing value for required argument ([0-9]+)`, `Invalid params`. A wrong-params reply proves the method exists.
- `MethodAvailabilityUnknown`: everything else.

Not-available is checked **first**, deviating from dshackle's ordering at `UpstreamRpcMethodsDetector.kt:54`.

`MethodBanHook` bans on `MethodNotAvailable`, behaviour identical to today. `MethodProbeDetector` uses all three.

## Caps: live methods accessor

`caps.DetectorInput.Methods` is currently a snapshot handed in at construction (`upstream.go:124` passes `creationData.upstreamMethods`), and `evm_caps/evm_head_sub_detector.go:70-72` reads it to decide the NewHeads/Logs caps from `HasMethod("eth_subscribe")` / `HasMethod("eth_getLogs")`. Once detection narrows the set, that snapshot is stale, and no later round would ever reach it.

```go
// MethodsSource yields the upstream's current method set. It is a function rather
// than a value because the set is not fixed for the upstream's lifetime: method
// detection narrows it at start, bans remove entries, re-detection replaces it.
type MethodsSource func() methods.Methods
```

`DetectorInput.Methods` becomes a `MethodsSource`; `CreateCapEventProcessor` takes one; `NewGenericUpstream` passes `func() methods.Methods { return upstream.upstreamState.Load().UpstreamMethods }`, which is valid at that point because `upstreamState` is populated at `upstream.go:89`, before the aggregator is built at `:116`. `evm_head_sub_detector` reads through it at evaluation time.

An accessor guarantees freshness whenever a cap detector evaluates, but does not itself trigger re-evaluation when the method set changes. Since detection is asynchronous, the first detection round can land *after* `evm_head_sub_detector` has already evaluated — and that detector is push-driven off the ws connector's state stream, so a methods-only change does not wake it. It would keep asserting a cap until the connector's next state change or the next resume.

That is acceptable for the current cap detectors, and the reason is worth recording rather than assuming: both methods they consult, `eth_subscribe` and `eth_getLogs`, are in the `eth` module, which no EVM node lacks. Detection can only strip them if `rpc_modules` omits `eth` entirely, at which point the upstream has no usable surface at all. So the race exists but is inert.

**Trigger condition for a future change:** if a cap detector is added that consults a method detection can plausibly strip — anything `debug_*`, `trace_*`, or module-gated — it needs to re-evaluate on methods change, by subscribing to `MethodsProcessor` the way it already subscribes to connector states. Not built now, because nothing needs it.

Bans land mid-flight and are likewise picked up on the detector's next emission.

Side effect worth having: a node without the subscribe surface correctly stops advertising `NewHeads`.

Touches `caps/caps_test.go` `DetectorInput` literals and `NewGenericUpstreamWithParams`.

## Tests

- **`internal/protocol`**: table test over `ClassifyMethodAvailability` — every not-available pattern, every params pattern, `-32601`, unrelated errors → `Unknown`, and a message matching both lists resolving to `MethodNotAvailable`.
- **`evm_methods`**: `RpcModulesDetector` — module attribution, `{eth,net,web3}` stripping `trace_*`/`debug_*`, no-prefix methods left alone, and errored/malformed/empty/unimplemented replies each returning nil. `MethodProbeDetector` — per-response tri-state; a probe that cannot be reached keeps its last answer while another probe answers in the same round; a conclusive answer replaces a retained one; nil while nothing has ever been learned; methods outside the chain's spec are never asked about.
- **`methods`**: `DetectableMethods` excludes `IsLocal()` methods. `GenericMethodsProcessor` publishes the union of its detectors' verdicts to a subscriber; `Start()` returns without waiting on detection; the first round is immediate and a second round follows after `delay` (injected short in the test, as `label_processor_test.go` does); an unchanged verdict publishes nothing on the second round while a changed one publishes; `NewGenericMethodsProcessor` returns nil for no detectors.
- **`event_processors`**: `MethodsEventProcessor` forwards a published set to the emitter as the event; `CreateMethodsEventProcessor` returns nil when the option is on. Follows `labels_event_processor_test.go`.
- **`upstream_events`**: unsupported and banned sets coexisting; unban not restoring an unsupported method; `config enable` overriding detection; a duplicate event not republishing.
- **`internal/config`**: strict → `false`, default → `true`, and chain-defaults/global overrides winning.
- **`internal/upstreams`**: an upstream started against a stubbed connector returning `{eth,net,web3}` eventually has no `trace_*`/`debug_*` in `GetSupportedMethods()` and still reports `net_version` and `eth_chainId`. Because detection is asynchronous this asserts on the upstream's event stream rather than reading state immediately after `Start()`, matching how `upstream_test.go` already tests ban/unban.

All tests live in the package that owns the target, so unexported functions are tested directly. No reflection into private fields.

## Docs

`docs/nodecore/05-upstream-config.md`: `disable-methods-detection` in the options table, plus a short subsection on how detection composes with `methods.enable` / `methods.disable` and why `enable` wins.

## Non-goals

- **Polkadot.** `rpc_methods` fits `MethodsDetector` unchanged, as a single stage with no probes, and is separate work.
- **Chains with no introspection.** Solana and Aztec expose nothing to query, so the only option is probing every spec method individually — dozens of real calls per upstream start. Bitcoin's `help` returns a human-readable text blob to scrape; Cosmos/Tendermint expose a REST index page. All fit the interface later; none are worth it now.
- **A dedicated config option for the detection interval.** `methods.DetectionInterval` is a constant. Adding `methods-detection-interval` to `chains.Options` is a small follow-up if an operator ever needs to tune it, but shipping a knob nobody has asked for is not worth the config surface.
- **Excluding detection traffic from the dimension and stats hooks.** Probe errors are recorded against the upstream and feed the error-rate rating function. This is pre-existing behaviour shared by every detector in the codebase, and dropping staging nudges the volume up; fixing it properly means plumbing an uninstrumented connector through `getChainSpecific`, which is its own change.
- **Blocking the upstream's start on detection.** An earlier draft resolved methods synchronously in `Start()`. Rejected: it buys a fraction of a second of correctness at the cost of a blocking network round in startup, and the window it closes is the over-enablement nodecore already lives with today.
- **Detection adding methods.** Covered under [Semantics](#semantics): the spec is the ceiling.
- **Per-connector method sets.** Detection runs over `internalRequestConnector` and its verdict applies to the upstream as a whole. Splitting a method set per connector is a separate concern that `methods.NewUpstreamMethods` already handles via `apiConnectorTypes`, and detection does not change it.
