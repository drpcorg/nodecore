# Hot reload of upstreams — design

- **Date:** 2026-10-08
- **Status:** Implemented — branch `arootman/hot-reload-upstreams`
- **Area:** `internal/reload` (new), `internal/upstreammetrics` (new), `pkg/reloadsignal` (new), `internal/upstreams` (`upstream_supervisor.go`, `upstream_reload.go`, `upstream.go`, `chain_supervisor.go`, `connectors`, `ws`), `internal/config` (`config.go`, `reload_config.go`), `internal/app`, `internal/dimensions`, `internal/rating`, `internal/upstreams/flow`, `test/e2e`

## 1. Problem

nodecore reads its config once. Every change of the upstream list - one node
added, one node retired - means a new process, and a new process means that
every client connection is closed. For a consumer that holds a long-lived gRPC
connection (`NativeCall`, `NativeSubscribe`, `SubscribeChainStatus`) that is the
worst kind of change: requests in flight fail, subscriptions drop, and the
consumer's view of *every* chain of this instance is rebuilt from scratch,
although only one chain changed.

Measured on `main` (4b083ba, nodecore 1.16.11), one mock EVM upstream, one gRPC
client:

- **Nothing reloads.** The config file was rewritten with a second chain; after
  8 s the new chain was still not served. A restart is the only way.
- **A restart loses work.** On `SIGTERM` the process exits in about 0.2 s. A
  `NativeCall` that was 1 s into a 5 s upstream call fails with
  `Unavailable ... error reading from server: EOF`; an in-flight HTTP call
  returns 408. The signal cancels the root context (`cmd/nodecore/main.go`),
  which cancels the upstream calls, and `App.Start` (`internal/app/app.go`)
  returns after the HTTP and health shutdown without waiting for the goroutine
  that runs the gRPC `GracefulStop` (`internal/server/grpc_server`).
- **The connection goes down.** The client's connection left `READY` on each
  restart (`IDLE -> CONNECTING -> TRANSIENT_FAILURE -> READY` when the redial
  raced the new process), its `SubscribeChainStatus` stream broke each time, and
  a 100 ms prober on the untouched chain saw up to 1.0 s of failures per restart.

None of this is needed, because the internals are already dynamic:

- `GenericUpstreamSupervisor` keeps upstreams in a concurrent map and starts
  each one in its own goroutine. Chain supervisors are created lazily on the
  first event of a chain and announced with `AddChainSupervisorEvent`, which
  the chain-status stream already follows.
- `protocol.RemoveUpstreamEvent` exists and `GenericChainSupervisor` handles it:
  it drops the upstream's state and recomputes the chain's status, head, methods
  and feeds. `Upstream` has `Stop()`.

What is missing is an operation that changes the upstream set of a running
supervisor, something that triggers it, and the removal of a few assumptions
that upstreams live as long as the process.

## 2. Goal

Adding or removing an upstream in the config file takes effect in a running
nodecore, and **client connections are not touched at all**: same process, same
listeners, same gRPC connections and streams; requests in flight on other
upstreams complete; a consumer of `SubscribeChainStatus` sees only the events of
the chains that actually changed.

A config file that can't be applied is ignored as a whole and never stops the
process.

### Non-goals

- **Reloading anything but the upstream list.** Ports, TLS, keys, auth, cache,
  storages, rate limit budgets and the routing settings of `upstream-config`
  keep their startup values. Each of them has its own consumers that captured
  the value at construction; making them swappable is a separate change per
  section.
- **Seamless replacement of a changed upstream.** A changed upstream is removed
  and started again (section 7). Add and remove are the seamless operations.
- **Graceful shutdown.** A restart still drops in-flight requests as described
  above. It is the same symptom with a different cause and is left as a
  follow-up (section 14).
- **A reload API.** No HTTP or gRPC endpoint that accepts an upstream list. The
  config file stays the single source of truth, so a restart always reproduces
  what a reload produced.
- **Removing a chain object.** A chain whose last upstream is gone stays known
  and unavailable (section 10).

## 3. Key decisions (settled -> implemented)

| Decision | Choice | Why |
|---|---|---|
| Unit of reload | The whole `upstream-config.upstreams` list, reconciled by upstream id | The operator edits a file, not a diff. Reconciling makes a reload idempotent and makes the file after a reload equal to the file a restart would read. |
| What counts as changed | `reflect.DeepEqual` of the upstream config **after defaults** | Defaults merge `chain-defaults.<chain>.poll-interval`/`options` and the `mode` fallbacks into each upstream, so a changed default is seen as a change of exactly the upstreams it affects. |
| Changed upstream | Remove, then start a new instance under the same id | One code path for everything. Starting the new instance first would need two live upstreams under one id, and ids key every per-upstream structure. |
| Trigger | `SIGHUP` always; file watch opt-in (`upstream-config.reload.watch-interval`) | A signal works everywhere a process manager can send one. A watch is needed where nothing can (a ConfigMap volume). Opt-in, because until now editing the file of a running nodecore had no effect. |
| Who owns `SIGHUP` | A leaf package subscribes in its `init` and never unsubscribes | An unsubscribed `SIGHUP` terminates the process. Once the docs tell operators to send it, it must not be fatal at any moment: not while nodecore starts, not while it shuts down. |
| Watch mechanism | Poll the file and compare a SHA-256 of its content | No new dependency, and independent of how the file is replaced: in-place write, rename, a symlink swap. inotify-style watchers lose a file that is replaced by rename. |
| Half-written files | The watch applies content only after reading it identical on two consecutive checks; `SIGHUP` applies immediately | Validation cannot catch this case: a YAML file cut at a line boundary is usually a valid, shorter config. |
| Validation | The whole file goes through the startup parser, defaults and validation; then the supervisor checks the list against the running process | A reload accepts exactly the files a restart would accept, so a reload can never leave the process in a state the next restart refuses. |
| Invalid file | Reject the whole reload, log at `error`, keep running | The alternative - applying the valid part - makes the running set depend on the order of mistakes. |
| What makes a file "handled" | A verdict on its content: applied, or rejected for what it says | The watch reports a file once. A refusal that is not about the content ("upstreams are not started yet") must not use that one report up, or a good file is never applied. |
| Restart-only settings changed | Warn, naming each setting, and still apply the upstream list. Only what a reload really leaves unapplied is named | Rejecting would hold back an upstream change because of an unrelated edit; applying silently would hide that part of the file is not in effect. A warning about a setting that *was* applied (a chain's `poll-interval`) sends the operator to restart for nothing. |
| Upstream index | Bound to the upstream id for the process lifetime | The index is embedded in sticky ids (`eth_newFilter`). A re-added or replaced upstream must keep resolving them, and an index must never be handed to a different upstream. |
| Who pauses and resumes an upstream | The goroutine that owns the upstream, not the supervisor's event loop | The event loop looked the upstream up by id. With ids that can be handed over to a new instance, the removal of the old instance stopped the new one (section 8). |
| Order of a removal | Removal event -> wait until the chain no longer routes to it -> drain and close connectors -> cancel the upstream's context | Closing first leaves a window in which requests are still routed to a closed connector. Cancelling the context first closes the websocket under the requests being drained and drops the command that ends its subscriptions. |
| Subscriptions on a removed websocket upstream | Ended explicitly: every operation is cancelled and `WsDisconnected` is published before `Stop` returns | A client must get an error, not silence: silence can't be told from a quiet chain. |
| Requests in flight on a removed upstream | Unary requests get up to 5 s to finish before a websocket or gRPC connection is closed; streams are ended with the connection; HTTP requests are never interrupted | Measured against a real gRPC node: without the wait every removal failed the 7-12 unary calls that were on the connection (section 12). Streams have no end to wait for. |
| Chain without upstreams | Kept, reported `UNAVAILABLE` | The chain-status protocol has no "chain removed" message; consumers already handle a status change, and the chain can come back on the same stream. |
| Per-upstream leftovers | Dropped on removal: dimension tracker entries and every metric series labelled with the upstream | A removed upstream must not keep reporting its last state, and one that comes back under the same id must not inherit old latency data. |
| Where metric series are dropped | One list, `internal/upstreammetrics`: a metric with an `upstream` label registers there where it is defined; one `Forget(id)` on removal; a test scans the sources | Cleanup spread over the packages that own the metrics missed one (`hedge_hit`) on the first attempt. |
| Sticky id created on an upstream removed in flight | The client gets an error | The id is on a node nodecore no longer talks to, and without the upstream index the next sticky request would cut the id's tail off as one. |
| Chain head when an upstream leaves | Always decided by the fork choice | An upstream that leaves without a head (a dead node) must not zero the head the others provide. |

## 4. Terminology

- **Managed set** — the upstreams the supervisor has been asked to run, by id,
  each with the config it was started from. It includes upstreams that failed to
  start, so that a reload does not retry them unless their config changes.
- **Owner goroutine** — the one goroutine per upstream that creates it, starts
  it, forwards its events and, when told to, removes it.
- **Reload** — one attempt to load the config file and apply its upstream list.
  Its result is `applied`, `unchanged` or `rejected`.
- **Restart-only section** — any part of the config other than
  `upstream-config.upstreams`.
- **Replace** — remove a running upstream and start a new instance with the same
  id and a different config.

## 5. Architecture

```
            SIGHUP          ticker (watch-interval)
               |                   |
               v                   v
        +----------------------------------+
        | reload.ConfigReloader            |   internal/reload
        |  read file -> sha256             |
        |  watch: same content twice?      |
        |  config.ParseAppConfig (full     |-- error --> log, metric, nothing changes
        |    defaults + validation)        |
        |  startup.RestartOnlyChanges(new) |-- differ --> warn, continue
        +----------------+-----------------+
                         | []*config.Upstream
                         v
        +----------------------------------+
        | GenericUpstreamSupervisor        |   internal/upstreams
        |  ApplyUpstreams                  |
        |   validateUpstreams              |-- error --> nothing changes
        |   diffUpstreams(managed, wanted) |
        |   removed/changed: cancel owner  |
        |   added/changed:   startUpstream |
        +---+--------------------------+---+
            |                          |
   owner goroutine (one per upstream)  |
   runUpstream:                        |
     wait for the previous instance    |
     CreateUpstream + Start            |
     forward events ------------------>+--> eventsChan --> processEvents
     on cancel: removeUpstream                               |
       RemoveUpstreamEvent ----------------------------------+
       wait until not routed                                 v
       upstreams.Delete, up.Stop()              GenericChainSupervisor (per chain)
       forget metrics / dimensions                drops state, recomputes status,
                                                  head, methods, feeds
                                                             |
                                                             v
                                      SubscribeChainStatus streams, routing, /status
```

Nothing above the chain supervisor changes. The servers, the execution flow and
the chain-status stream already work from chain state that moves at runtime; a
reload is one more source of the same events.

## 6. Config reloader (`internal/reload`, `internal/config`)

`config.NewAppConfig` is split so that a reload goes through the same code as
startup:

```go
func ConfigPath() string                              // NODECORE_CONFIG_PATH or the default
func LoadAppConfig(configPath string) (*AppConfig, error)
func ParseAppConfig(file []byte) (*AppConfig, error)  // unmarshal + setDefaults + validate
```

The new setting:

```yaml
upstream-config:
  reload:
    watch-interval: 5s   # 0 (default) = no watch; minimum 100ms
```

`SIGHUP` is subscribed to by `pkg/reloadsignal`, in its package `init`, and the
subscription is never released; `main` passes the channel through `NewApp`. A
process that has not subscribed is terminated by the signal, and subscribing
inside the reloader left exactly that for the startup and for the shutdown.
The package imports only the standard library so that its `init` runs early:
it is the 28th of 371 package initializations, 4 ms into the process, where
`main` begins after about 30 ms. A `SIGHUP` in those first milliseconds is
still fatal; a parent that needs that covered starts nodecore with `SIGHUP`
ignored, and the subscription takes the signal back.

`App.Start` runs `StartUpstreams` and then `ConfigReloader.Run` in one
goroutine, so a reload always finds started upstreams. `Run` serves the signal
channel - a signal that arrived during the startup is waiting in it - and,
when `watch-interval` is set, a ticker:

```go
func (r *ConfigReloader) watch() {
	file, err := os.ReadFile(r.path)
	if err != nil {            // being replaced right now: look again later
		r.candidate = [sha256.Size]byte{}
		return
	}
	sum := sha256.Sum256(file)
	if sum == r.handled {      // already applied or already rejected
		return
	}
	if sum != r.candidate {    // first sight of this content
		r.candidate = sum
		return
	}
	r.apply(file)              // seen twice in a row
}
```

`SIGHUP` calls `Reload`, which reads the file and applies it without the
two-look rule. A mutex serializes the two triggers.

`handled` is set by `apply` once there is a verdict on the content: applied, or
rejected for what it says (it does not parse, does not validate, or the
supervisor refuses the list). A broken file is therefore reported once and not
on every tick. `upstreams.ErrUpstreamsNotStarted` is not a verdict: the content
stays unhandled, nothing is counted as rejected, and the watch applies it on
its next check.

`apply`:

1. `config.ParseAppConfig` inside a `recover`. The startup path is allowed to
   panic on a config it can't work with; a reload must only refuse it.
2. `applier.ApplyUpstreams(loaded.UpstreamConfig.Upstreams)`.
3. `startupConfig.RestartOnlyChanges(loaded)` — names the settings that differ
   and that the reload did not apply, and logs them as a warning.
4. Metrics and one log line with the diff.

`RestartOnlyChanges` names only what a reload leaves unapplied. Whole sections
for everything outside `upstream-config`; inside it, of `chain-defaults` only
the per-chain routing settings, each on its own
(`upstream-config.chain-defaults.<chain>.dispatch`, `.label-balancing`,
`.balancing-strategy`, `.local-subscriptions`, `.validate-lag`), because
`poll-interval` and `options` are applied through the upstreams; and `mode`,
for the `dispatch` and `validate-lag` defaults it decides at request time.

It compares by YAML form, not with `reflect.DeepEqual`. The running config carries state that is built while the
process works - `ScorePolicyConfig` caches its compiled score function on first
use - and a struct comparison reported that as a change on every reload (found
by the e2e test, section 11).

### chain-defaults, mode and failsafe settings on reload

- `chain-defaults.<chain>.poll-interval`, `chain-defaults.<chain>.options` and
  `mode` are inputs of the per-upstream defaults. They are folded into each
  upstream's config before the diff, so changing them **replaces the upstreams
  that inherit the changed value** and leaves the others alone. A change of
  `poll-interval` or `options` is not warned about. A change of `mode` is,
  because `mode` also decides the chains' `dispatch` and `validate-lag`
  defaults while requests are served, and that part is not applied.
- An upstream's own `failsafe-config` is part of its config: changing it
  replaces that upstream.
- The global `upstream-config.failsafe-config` builds the flow executor once at
  startup, and `chain-defaults.<chain>.dispatch`, `label-balancing`,
  `balancing-strategy`, `local-subscriptions` and `validate-lag` are read by the
  execution flow and the chain supervisor from the startup config. They are
  **not reloaded**.

## 7. Applying an upstream list (`internal/upstreams/upstream_reload.go`)

```go
type UpstreamsDiff struct {
	Added   []string
	Removed []string
	Changed []string // same id, different config: replaced
}

// in UpstreamSupervisor
ApplyUpstreams(upstreamConfigs []*config.Upstream) (UpstreamsDiff, error)
```

`ApplyUpstreams` takes `applyMu`, validates, diffs and then only *initiates* the
changes:

```go
for _, id := range slices.Concat(diff.Removed, diff.Changed) {
	managed := b.managed[id]
	delete(b.managed, id)
	b.retired[id] = managed.done
	managed.cancel()
}
for _, upConfig := range upstreamConfigs {   // added and changed
	if toStart.ContainsOne(upConfig.Id) {
		b.startUpstream(upConfig, true)
	}
}
```

It does not wait for the upstreams to stop or to become available. Starting an
upstream runs its settings validation against the node, which can take as long
as `internal-timeout`; holding the reload for that would make one slow node
delay every other change in the same file.

`StartUpstreams` uses the same `startUpstream`, so an upstream added by a reload
is started through exactly the startup path.

`validateUpstreams` exists because `ApplyUpstreams` is an entry point of its
own: what it lets through goes straight into `CreateUpstream`, where a bad list
panics, and it must not depend on its caller having validated a config file.

- The checks of the list as a whole - not empty, no missing or duplicate ids,
  supported chains - are `config.ValidateUpstreamList`, the same function the
  config validation starts with, so both report a problem in the same words.
- Two checks only the running process can make:
  - a `rate-limit-budget` must exist in the budget registry that was built at
    startup. The file validation accepts a budget that is defined in the new
    file; `createRateLimiter` would then `log.Panic` on it;
  - enough free upstream indices for the new ids.

A panic that still happens while an upstream added by a reload is being created
is recovered in `createAndStartUpstream` and reported as a failed start of that
upstream - and the upstream is taken down, not just forgotten. A panic in
`Start` comes after the connectors are started, so the recover calls
`up.Stop()`; `CreateUpstream` cancels its context when it panics half way, which
releases the websocket request registry and the rate limit auto-tuner already
bound to it. At startup the same panic still stops the process, as before.

## 8. Upstream ownership and removal (`internal/upstreams/upstream_supervisor.go`)

Before, `StartUpstreams` spawned a goroutine per upstream that listened on the
supervisor context and could never be told to stop. Now each upstream has a
`managedUpstream{config, cancel, done}` and `runUpstream` owns it:

```go
func (b *GenericUpstreamSupervisor) runUpstream(ctx, managed, previous, upstreamIndex, reloaded) {
	defer close(managed.done)
	if previous != nil {
		<-previous                    // the old instance of this id is completely gone
	}
	if ctx.Err() != nil {
		return
	}
	up, upSub, err := b.createAndStartUpstream(upConfig, upstreamIndex, reloaded)
	...
	for {
		select {
		case <-ctx.Done():
			upSub.Unsubscribe()
			if b.ctx.Err() == nil {   // removed, not a shutdown
				b.removeUpstream(up)
			}
			return
		case upstreamEvent, ok := <-upSub.Events:
			if ok {
				b.forwardUpstreamEvent(up, upstreamEvent)
			}
		}
	}
}
```

`removeUpstream`:

```go
b.publishEvent(protocol.UpstreamEvent{Id: up.GetId(), Chain: up.GetChain(), EventType: &protocol.RemoveUpstreamEvent{}})
b.waitUntilNotRouted(up)      // chain supervisor dropped its state, at most 2s
b.upstreams.Delete(up.GetId())
up.Stop()                     // connectors drain and close, see below
b.forgetUpstream(up)          // dimensions + metric series
```

`up.Stop()` was never called on a serving upstream before this change, so
neither its order nor what its connectors do on `Stop` had mattered.

`GenericUpstream.Stop` stops the processors, then the connectors, and cancels
the upstream's context **last**. The websocket loop and the websocket request
registry both live on that context. With the cancel first, the loop closed the
socket at once - so the drain below waited on a dead socket until its timeout,
and the requests on it ran into their own timeouts - and the command that
cancels the registry's operations was dropped, so a subscription on the removed
upstream got neither an error nor a closed channel.

`GenericWsProcessor.Stop` is synchronous: it ends the connection loop and waits
(at most 5 s, for a loop caught in a dial) until the loop has closed the socket,
`CancelAll` has cancelled every request and subscription - `CancelAll` now
returns only when that is done - and `WsDisconnected` is published, as it is for
a disconnect seen by the reader. A subscription source reacts to either the
closed channel or the state and sends its clients the terminal failure. The
reconnect backoff is bound to the context, so a loop that is between two
attempts stops at once instead of sleeping the backoff out.

What the connectors do:

- **gRPC and websocket** connectors own one connection, and closing it fails
  every call on it. Every connector is wrapped in an `ObserverConnector`, which
  now counts the unary requests between `SendRequest` and its return. Its `Stop`
  waits for that count to reach zero, for at most `stopDrainTimeout` (5 s),
  before it stops the connector underneath. Because routing has already dropped
  the upstream, the count can only go down. Streams and subscriptions are not
  counted: they never end by themselves and are ended by the close.
- **HTTP** connectors (`json-rpc`, `rest`, `tendermint`) never interrupt a
  request, so they are not waited for. Their `Stop` was empty and left the
  keep-alive connections to the removed node open until the idle timeout; it
  now calls `CloseIdleConnections`.
- `GenericUpstream.Stop` stops its connectors side by side, so the waits of
  several connectors of one upstream do not add up.

Three properties make this safe:

1. **The removal event is the last event of the instance.** The owner
   unsubscribes from the upstream first and only then publishes
   `RemoveUpstreamEvent` into the same `eventsChan` its forwarded events went
   through. Both queues behind it are FIFO, so no late `StateUpstreamEvent` can
   put the upstream back into the chain. `up.Stop()` itself publishes nothing:
   it cancels the upstream's own event loop.
2. **One instance per id at a time.** A new instance of an id waits on the
   `done` channel of the previous one (`retired`), so the old removal event is
   queued before the new instance can publish anything.
3. **Pause and resume act on the instance, not on the id.** `processEvents`
   used to resolve `event.Id` through `GetUpstream` to call `PartialStop` on a
   `RemoveUpstreamEvent` and `Resume` on a `ValidUpstreamEvent`. With property 2
   the lookup can already return the *new* instance when the removal of the old
   one is processed, and the new upstream would be stopped right after its
   start. Both calls moved to `forwardUpstreamEvent`, which runs in the owner
   goroutine and holds the instance. `processEvents` also no longer creates a
   chain supervisor for a removal event of a chain that never appeared.

## 9. What assumed that upstreams live for the whole process

| Place | Assumption | Change |
|---|---|---|
| `StartUpstreams` goroutine | Exits only with the supervisor context | Owner goroutine with its own cancel and `done` (section 8) |
| `upstreamIndicesCounter` | Indices are handed out once, in config order | `upstreamIndices` map: an id keeps its index; new ids take the next one; the overflow check moved to validation |
| `processEvents` | `GetUpstream(event.Id)` is the upstream that sent the event | Pause/resume moved to the owner goroutine |
| `executeUnaryRequest` (`flow/request_processor.go`) | `GetUpstream` of a just-selected id is never nil | Returns `NoAvailableUpstreamsError` |
| `GenericDimensionTracker` | Entries are never removed | `RemoveUpstream(chain, id)` drops the entries and the `nodecore_upstream_*` request and lag series |
| Metrics with an `upstream` label (14 vectors in 7 packages) | A series lives forever | Listed in `internal/upstreammetrics` at their definition; `Forget(id)` on removal |
| `ObserverConnector.Stop`, `GenericUpstream.Stop` | `Stop` is not called while requests are being served | Unary requests in flight are drained before a gRPC or websocket connection is closed (section 8) |
| `HttpConnector.Stop` | Nothing to release | Idle keep-alive connections are closed |
| `GenericUpstream.Stop` | The context can go first | Connectors first, context last (section 8) |
| `GenericWsProcessor.Stop`, `CancelAll` | Fire and forget | Synchronous; the loop reports `WsDisconnected` on its way out |
| `updateHead` (`chain_supervisor.go`) | An event with an empty head resets the chain head | It goes through the fork choice; the head is dropped only when no upstream has one |
| `StickyRequestProcessor` | The upstream of a response exists | A sticky create whose upstream is gone returns an error |
| `createAndStartUpstream` recover | Forgetting a failed upstream is enough | It is stopped |

Checked and left alone:

- **Rate limiting.** There is no per-upstream entry in
  `RateLimitBudgetRegistry`: an inline `rate-limit` builds a budget owned by the
  upstream, a named budget is shared and stays, and the auto-tune goroutine
  already ends with the upstream's context.
- **Stats.** `StatsService` aggregates request results by key and flushes them
  periodically; nothing is kept per upstream between flushes.
- **Other `GetUpstream` callers** (`method_hook`, `fanout`, `not_null`,
  `integrity`, `selectors`, `sub_aggregation`, `pending_tx_source`,
  `label_group_strategy`, `execution_flow`) already handle nil.
- **Rating order.** `sortedUpstreams` can name a removed upstream until the next
  calculation; selection skips ids without state in the chain supervisor.

## 10. Edge cases

- **Last upstream of a chain removed.** The chain supervisor stays, with no
  upstreams and status `Unavailable`. The chain-status stream sends a status
  event without a head; `/status` lists the chain as unavailable; requests get
  the usual "no available upstreams" error. When an upstream of the chain is
  added again the same chain object and the same streams report it available.
  nodecore keeps answering `eth_chainId`-style requests it serves itself for
  such a chain.
- **Chain appears.** First event of the new upstream creates the chain
  supervisor and publishes `AddChainSupervisorEvent`; every open
  `SubscribeChainStatus` stream subscribes to it and sends a full response once
  the chain has a head.
- **Replace of the only upstream of a chain.** The chain is unavailable between
  the removal and the end of the new instance's startup validation.
- **Removed while starting.** The owner finishes `CreateUpstream` and `Start`
  and then removes the upstream right away. A removal can therefore take as long
  as a start.
- **Add, remove, add of one id in quick succession.** Each instance waits for
  the previous one; an instance cancelled while waiting exits without starting.
- **`SIGHUP` at any moment.** Served when the reloader runs; one that arrives
  during the startup waits in the channel, one that arrives during the shutdown
  is dropped. Never fatal after the first milliseconds of the process
  (section 6).
- **Removing a dead upstream.** An upstream that never reported a head leaves
  with an empty head in its removal event. The chain head stays what the fork
  choice has from the other upstreams; before, it was reset to zero until the
  next block, for the status stream and the integrity methods alike.
- **How long a removal takes.** Routing stop (at most 2 s, normally
  milliseconds), then the drain of a gRPC or websocket connector (at most 5 s,
  nothing without requests in flight), then the close (milliseconds; at most
  5 s for a websocket caught in a dial). Measured with real nodes: 0.02-0.06 s.
  A replaced upstream waits for all of it before its new instance starts, and
  then for the startup validation.
- **Sticky create in flight.** Its upstream is gone when the answer arrives:
  the client gets the "no available upstreams" error instead of an id.
- **Requests in flight on a removed upstream.** Over HTTP the request finishes
  on the caller's context. Over gRPC and websocket a unary request gets up to
  5 s; one that is still running then fails with gRPC `Canceled` (`grpc: the
  client connection is closing`) or the websocket equivalent. A late request
  can re-create a few request-counter series for the removed upstream.
- **Streams on a removed upstream.** Ended when the connection is closed. A
  stream on a gRPC connector: the client receives `Canceled` as the terminal
  frame (0.2 s after the reload in the measurements). A subscription on a
  websocket connector: a `NativeSubscribe` client receives `Internal:
  subscription total failure`, a websocket client has its connection to
  nodecore closed (0.01-0.05 s after the reload) - what they get when an
  upstream's websocket drops by itself. Either can resubscribe to the remaining
  upstreams.
- **Replace with slow requests in flight.** The new instance of an id starts
  only after the old one is stopped, so a gRPC or websocket upstream that is
  being replaced can stay out for up to the 5 s drain on top of its startup
  validation.
- **Probes of a removed upstream.** Its head, bound and label detectors are
  cancelled; the ones that were in the middle of a request log a `context
  canceled` error, some of them a few seconds later.
- **File replaced by rename.** Handled by reading by path on every check. A
  container that bind-mounts the single file keeps the old inode and never sees
  the new content; the directory has to be mounted. Documented.
- **File missing for a moment.** The watch treats a read error as "look again";
  `SIGHUP` reports it as a rejected reload.
- **Truncated file that is still valid.** Covered by the two-look rule of the
  watch only. With `SIGHUP` it is the sender's job to signal a complete file.
- **A rejected file followed by nothing.** The running set stays older than the
  file; `nodecore_config_last_reload_successful` stays 0 until a valid file is
  loaded.
- **Reload before `StartUpstreams` ran.** `ApplyUpstreams` returns
  `ErrUpstreamsNotStarted`. The app starts the reloader after the upstreams, so
  this is for other callers; the reloader treats it as "not yet" and not as a
  rejected file.
- **An upstream that panics while starting, reloaded again and again.** Each
  attempt is stopped completely; nothing accumulates.
- **Shutdown during a removal.** Every wait in the removal path also selects on
  the supervisor context.

## 11. Testing

Unit, `internal/upstreams`:

- `diffUpstreams`: same list, equal configs under different pointers, order,
  added, removed, changed connector, changed option behind a pointer, all three
  at once.
- `validateUpstreams`: empty list, nil entry, missing id, duplicate id, unknown
  chain, unknown rate limit budget, index overflow.
- `upstreamIndex`: an id keeps its index, including when no free index is left.
- `ApplyUpstreams` against in-process mock EVM nodes, with the real
  `CreateUpstream`:
  - a chain is added, announced with `AddChainSupervisorEvent`, removed (chain
    kept, unavailable, upstream stopped and unregistered), and added again
    (same chain supervisor, same upstream index), while the upstream of the
    other chain stays the same object;
  - add and remove within one chain;
  - a changed upstream is replaced and ends up available with its new config -
    the regression test for property 3 of section 8;
  - a rejected list changes nothing;
  - an upstream whose head comes from a websocket subscription is removed: the
    websocket is closed and is not reconnected;
  - an HTTP request in flight on a removed upstream completes;
  - an upstream with a gRPC connector, in the shape of a Tron node (JSON-RPC
    head, gRPC next to it), against an in-process gRPC server on a real TCP
    port that counts its connections: a unary call in flight is answered, an
    open stream ends with an error frame, the connection is closed, the node is
    not dialled again, and a replace leaves exactly one connection.
- `internal/upstreams/connectors`: `ObserverConnector.Stop` waits for requests
  in flight on gRPC and websocket connectors, gives up at the timeout, and does
  not wait on HTTP connectors or when nothing is in flight.

  - a unary request in flight on the websocket of a removed upstream is
    answered and the removal does not sit out the drain timeout; a
    subscription on it gets its channel closed and the connector reports
    `WsDisconnected` (both fail on the cancel-first order);
  - an upstream whose `Start` panics is stopped; a panic inside
    `CreateUpstream` leaves no request registry goroutine behind;
  - the supervisor's list checks report the same errors as the config
    validation.
- `chain_supervisor`: removing an upstream with an empty head, and an empty
  head event of one upstream, keep the chain head; the head goes when the last
  one does.

Unit, elsewhere:

- `internal/upstreammetrics`: `Forget` drops the series of one upstream; a scan
  of the sources fails for any metric vector with an `upstream` label that is
  not in the list.
- `pkg/reloadsignal`: a `SIGHUP` sent to the test process is delivered instead
  of terminating it; the package imports only the standard library.

- `internal/reload`: a reload applies the list; broken files (not YAML, empty,
  cut in the middle of an upstream, unknown chain, duplicate id, no upstreams,
  invalid server setting) are rejected without calling the supervisor; recovery
  after a rejected file; the two-look rule; a rejected file is reported once;
  a missing file; the ticker-driven `Run`; a signal that was waiting before
  `Run` started; a transient refusal is retried by the watch and is not a
  rejection, while a list refused by the supervisor is reported once.
- `internal/config`: `watch-interval` default and minimum; two loads of the full
  example config show no restart-only change, also after the running config
  compiled its score function; the upstream list is not a restart-only change;
  a chain's `poll-interval` and `options` are not either; the routing settings
  of a chain are named one by one.
- `internal/dimensions`: per-upstream cleanup, scoped to the chain.
- `internal/upstreams/flow`: an upstream that disappears after selection; a
  sticky create whose upstream disappears in flight returns an error.

E2E, `test/e2e/grpc` (`TestGrpcUpstreamsReloadKeepsConnection`): one gRPC
connection and one `SubscribeChainStatus` stream are kept for the whole test.

1. Start with an ethereum upstream; polygon requests fail.
2. A 3 s request to ethereum is sent; the config file is rewritten with a
   polygon upstream. The **same stream** delivers a full polygon response, a
   polygon request is served by the polygon node, the slow request succeeds.
3. Another slow request; polygon is removed from the file. The stream delivers
   polygon `UNAVAILABLE`, polygon requests fail, the slow request succeeds.
4. Six broken files in a row (not YAML, unknown chain, duplicate id, no
   upstreams, half-written, invalid server setting): each is rejected in the
   log, ethereum keeps serving, polygon stays absent.
5. A valid file brings polygon back.
6. The connection never left `READY` and the stream never returned an error.

`TestGrpcUpstreamsReloadEndsSubscriptionsOfRemovedUpstream`: a `NativeSubscribe`
stream and a websocket client subscription, both served by the websocket of one
upstream (local subscriptions off for the chain), receive events; the upstream
is removed; both must end with an error within 15 s. Against an image built
before the stop-order fix the gRPC stream stays open and the test fails; with
it the stream ends with `Internal: subscription total failure` and the
websocket client is disconnected, 0.4 s after the file is written (0.2 s watch).

The e2e tests run on `test/e2e/internal/mocknode`, a small JSON-RPC node added
with this change: it has no chain data behind it and needs no fork provider, so
the tests need no credentials, and it can serve a second chain id and answer one
method slowly, which a forked node cannot. It serves JSON-RPC over HTTP and,
on the same port, over a websocket with `eth_subscribe`; the websocket side is
a few dozen lines of RFC 6455 so that the image builds without dependencies.

## 12. Real-data validation

Real processes on one machine: the nodecore binary built from source, two mock
EVM nodes (ethereum with a 5 s `eth_getBalance`, polygon), and a Go client that
keeps one gRPC connection, one `SubscribeChainStatus` stream, a prober sending an
ethereum request every 100 ms, and a slow ethereum request in flight at each
change. The config file is replaced by write-and-rename. `watch-interval: 1s`.

| | `main` (4b083ba), change applied by restart | this branch, change applied by reload |
|---|---|---|
| Config change takes effect by itself | no (not after 8 s) | yes, 2.0 s after the write (two looks at 1 s) |
| Process | 3 pids over the run | 1 pid |
| In-flight 5 s request, polygon added | failed after 1.0 s, `Unavailable ... EOF` | OK after 5.0 s |
| In-flight 5 s request, polygon removed | failed after 1.0 s, `Unavailable ... connection reset by peer` | OK after 5.0 s |
| Connection state after the first `READY` | `IDLE, CONNECTING, TRANSIENT_FAILURE, READY, IDLE, CONNECTING, READY` | no transition |
| `SubscribeChainStatus` stream | broken at each restart (2 times) | never broken |
| Stream content at the change | full responses for every chain again | polygon `full=true AVAIL_OK`, later polygon `AVAIL_UNAVAILABLE`; nothing for ethereum |
| Ethereum prober | 258 ok, 10 failed, longest outage 1011 ms | 157 ok, 0 failed |

The same binary was then put behind a real consumer of the dshackle protocol: a
gateway whose aggregator subscribes to `SubscribeChainStatus` and whose proxy
routes client requests as `NativeCall` over a pool of gRPC connections, built
from source and run locally with its storage dependencies. Three loops of 1.2 s
ethereum requests ran through the gateway while polygon was added, removed,
added and removed again in nodecore's config file:

- polygon became routable through the gateway 5.7 s after the first add and
  3.8 s after the second (2 s of that is the watch), and stopped being routable
  2.1 s after each removal; the gateway's per-chain availability went
  `1 -> 5 -> 1 -> 5` for polygon and stayed `1` for ethereum;
- the gateway logged the new chain as added once and logged no stream error, no
  resubscribe and no reconnect;
- the 5 TCP connections from the gateway to nodecore (1 for status, 4 for
  calls) were the same 5 connections, by local port, before and after;
- 58 of 58 slow ethereum requests through the gateway succeeded.

The re-add matters: this consumer drops the status of a message that also
carries a head once it knows the chain. nodecore publishes head changes and
state changes as separate messages (the reason the periodic resync snapshot has
no head), so the status change of a re-added chain arrives on its own and is
applied.

### gRPC-connector upstreams

The connector that matters most for removal is the gRPC one, so the reload was
also run against real nodes over their gRPC ports, in the connector shapes used
in production: a Sui node (a single `grpc` connector), a Celestia consensus node
(`rest` + `grpc` + `tendermint`) and a Tron node (`json-rpc` + `grpc` +
`grpc-additional`), next to the mock ethereum upstream. Requests went through
the gRPC ingress with grpcurl.

- **Add.** All three chains were available within 1 s of the reload and served
  over gRPC: `sui.rpc.v2.LedgerService/GetServiceInfo`,
  `cosmos.base.tendermint.v1beta1.Service/GetNodeInfo` and
  `protocol.Wallet/GetNowBlock2` returned the nodes' answers.
- **Remove, with calls in flight.** A 5 s HTTP request on ethereum finished
  normally (`200` after 5.0 s). A `SubscribeCheckpoints` stream on Sui ended
  0.2 s after the reload with `Canceled: grpc: the client connection is
  closing`. Twelve callers sending unary Sui calls back to back, three runs:

  | | unary calls cancelled under the removal | calls sent after it |
  |---|---|---|
  | without the drain | 12, 7, 12 | `Unavailable: no available upstreams` |
  | with the drain | 0, 0, 0 | `Unavailable: no available upstreams` |

- **Connections.** With the three chains added the process held 1 TCP
  connection to the Sui node, 4 to the Celestia node and 9 to the Tron node.
  Two seconds after the removal it held none (before `CloseIdleConnections`, 8
  idle HTTP connections to the Tron node and 1 to the Celestia node were still
  open at that point), and none appeared during the following 45 s, although
  the gRPC reconnect backoff is at most 30 s. No goroutine of the gRPC client
  was left.
- **Change.** A label added to the Sui upstream replaced it; one connection to
  the node before and after.
- **Leaks.** After one add/remove cycle of all three upstreams and a 45 s
  settle the process had 44 goroutines; after ten more cycles and another 45 s
  it had 44, with no stack that grew and no connection to the removed nodes.

### Websocket upstreams

The websocket path was run against a real Polygon node that serves HTTP and
websocket on one port, in two shapes: the production one (`json-rpc` +
`websocket`, head over the websocket) and a websocket-only upstream, where
unary requests travel over the websocket too. Local subscriptions were off for
the chain, so client subscriptions were served by the node. In flight at the
removal: a `NativeSubscribe` stream, a websocket client subscription, and 12
callers sending `eth_getBalance` back to back.

| | cancel first (as first written) | connectors first |
|---|---|---|
| `NativeSubscribe` stream | silent; open until the process ended, 58 s later | `Internal: subscription total failure` 0.01-0.05 s after the removal |
| Websocket client subscription | silent; the client gave up after 36 s | connection closed 0.01-0.05 s after the removal |
| Unary calls in flight, websocket-only | all 12 hung until the client's 20 s timeout | 0 failed |
| Removal time, websocket-only | 5.03 s (the whole drain timeout) | 0.06 s |
| Removal time, production shape | 0.02 s | 0.02 s |
| Unary calls in flight, production shape (they use `json-rpc`) | 0 failed | 0 failed |
| Connections to the node after the removal | 0 | 0 |

`SIGHUP` was sent to a real process at chosen moments: 3-10 ms after `exec` it
is still fatal (the Go runtime has not reached the subscribing `init`); from
20 ms on it is served as a reload once the upstreams are started, and a
`SIGHUP` right after `SIGTERM` no longer changes the exit code (0). Before, a
`SIGHUP` before the reloader started ended the process with exit code 129.

## 13. Key code references

- `internal/reload/config_reloader.go` — `ConfigReloader`: `Run`, `Reload`,
  `watch`, `apply`, the three metrics.
- `pkg/reloadsignal` — the `SIGHUP` subscription.
- `internal/upstreammetrics` — `MustRegister`, `Track`, `Forget`.
- `internal/config/config.go` — `ConfigPath`, `LoadAppConfig`, `ParseAppConfig`.
- `internal/config/reload_config.go` — `ReloadConfig`, `RestartOnlyChanges`.
- `internal/upstreams/upstream_reload.go` — `UpstreamsDiff`, `diffUpstreams`,
  `ApplyUpstreams`, `validateUpstreams`, `ErrUpstreamsNotStarted`;
  `internal/config/upstream_config.go` — `ValidateUpstreamList`.
- `internal/upstreams/upstream_supervisor.go` — `managedUpstream`,
  `startUpstream`, `upstreamIndex`, `runUpstream`, `createAndStartUpstream`,
  `forwardUpstreamEvent`, `removeUpstream`, `waitUntilNotRouted`,
  `forgetUpstream`, `processEvents`.
- `internal/upstreams/chain_supervisor.go` — the existing `RemoveUpstreamEvent`
  handling this change relies on; `updateHead`.
- `internal/upstreams/ws/ws_processor.go` — `Stop`, `stopLoop`;
  `request_registry.go` — `CancelAll`.
- `internal/upstreams/connectors/observer_connector.go` — the in-flight count
  and the drain in `Stop`; `http_connector.go` — `Stop`;
  `internal/upstreams/upstream.go` — `Stop`.
- `internal/dimensions/tracker.go` — `RemoveUpstream`.
- `internal/upstreams/flow/request_processor.go` — nil guard;
  `sticky_request_processor.go` — error for a sticky create on a removed
  upstream.
- `internal/app/app.go` — the reloader is created in `NewApp` and started in
  `Start`.
- `test/e2e/grpc/upstreams_reload_e2e_test.go`,
  `upstreams_reload_subscriptions_e2e_test.go`,
  `test/e2e/internal/mocknode`, `test/e2e/internal/harness` (`StartMockNode`,
  `Nodecore.WriteConfig`, `Nodecore.LogCount`).
- `docs/nodecore/05-upstream-config.md` (`reload`),
  `docs/nodecore/08-prometheus-metrics.md` (Config Reload Metrics).

## 14. Open questions / future

- **Graceful shutdown.** `SIGTERM` should stop accepting, let in-flight requests
  finish and wait for `GracefulStop`. It needs the request path to stop
  depending on the root context, which is why it is not part of this change.
- **Seamless replace.** Start the new instance under a temporary identity, wait
  until it is available, then swap. Needs upstream identity that is not the
  config id.
- **Handing streams over.** A stream or subscription served by a removed
  upstream is ended and the client has to resubscribe. Moving it to another
  upstream of the chain without the client noticing is possible only for the
  aggregated subscriptions, and is not attempted.
- **Drain timeout.** The 5 s wait for unary requests on a gRPC or websocket
  connector is a constant; it could become a setting if a chain has legitimate
  calls that run longer.
- **Reloading the routing settings** (`chain-defaults` dispatch and balancing,
  global `failsafe-config`, rate limit budgets). The reloader already detects
  these changes; the consumers would have to read them through something
  swappable.
- **Removing chain objects.** Would need a "chain removed" event for
  `SubscribeChainStatus` consumers; today the cost is one idle chain supervisor
  per chain that was ever configured.
- **Series named after an upstream.** The `chanutil_*` metrics label their
  series with a `source` that embeds the upstream id
  (`<id>_upstream`, `upstream_supervisor_<id>_updates`). They have no
  `upstream` label and are not dropped on removal.
- **`nodecore_ratelimiter_auto_tune_tuned_rate_limit`** is documented but was
  never registered with Prometheus, before this change and after it. It is in
  the list of per-upstream metrics; exposing it is a separate fix.
- **The first milliseconds of the process.** `SIGHUP` is fatal until the
  runtime reaches the subscribing `init` (section 6).
- **Should the watch be on by default?** It is off to keep the behaviour of
  existing deployments, where editing the file had no effect until a restart.
- **Reload outcome on the health server.** `/status` could show the time and
  result of the last reload; today that is only in the metrics and the log.
