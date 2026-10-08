# Hot reload of upstreams — design

- **Date:** 2026-10-08
- **Status:** Implemented — branch `arootman/hot-reload-upstreams`
- **Area:** `internal/reload` (new), `internal/upstreams` (`upstream_supervisor.go`, `upstream_reload.go`), `internal/config` (`config.go`, `reload_config.go`), `internal/app`, `internal/dimensions`, `internal/rating`, `internal/upstreams/flow`, `test/e2e`

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
| Watch mechanism | Poll the file and compare a SHA-256 of its content | No new dependency, and independent of how the file is replaced: in-place write, rename, a symlink swap. inotify-style watchers lose a file that is replaced by rename. |
| Half-written files | The watch applies content only after reading it identical on two consecutive checks; `SIGHUP` applies immediately | Validation cannot catch this case: a YAML file cut at a line boundary is usually a valid, shorter config. |
| Validation | The whole file goes through the startup parser, defaults and validation; then the supervisor checks the list against the running process | A reload accepts exactly the files a restart would accept, so a reload can never leave the process in a state the next restart refuses. |
| Invalid file | Reject the whole reload, log at `error`, keep running | The alternative - applying the valid part - makes the running set depend on the order of mistakes. |
| Restart-only sections changed | Warn, naming the sections, and still apply the upstream list | Rejecting would hold back an upstream change because of an unrelated edit; applying silently would hide that part of the file is not in effect. |
| Upstream index | Bound to the upstream id for the process lifetime | The index is embedded in sticky ids (`eth_newFilter`). A re-added or replaced upstream must keep resolving them, and an index must never be handed to a different upstream. |
| Who pauses and resumes an upstream | The goroutine that owns the upstream, not the supervisor's event loop | The event loop looked the upstream up by id. With ids that can be handed over to a new instance, the removal of the old instance stopped the new one (section 8). |
| Order of a removal | Removal event -> wait until the chain no longer routes to it -> close connectors | Closing first leaves a window in which requests are still routed to a closed connector. |
| Chain without upstreams | Kept, reported `UNAVAILABLE` | The chain-status protocol has no "chain removed" message; consumers already handle a status change, and the chain can come back on the same stream. |
| Per-upstream leftovers | Dropped on removal: dimension tracker entries and every metric series labelled with the upstream | A removed upstream must not keep reporting its last state, and one that comes back under the same id must not inherit old latency data. |

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

`ConfigReloader.Run` is started by `App.Start` next to `StartUpstreams`. It
handles `SIGHUP` and, when `watch-interval` is set, a ticker:

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

`handled` is set for rejected content too, so a broken file is reported once and
not on every tick. `SIGHUP` calls `Reload`, which reads the file and applies it
without the two-look rule. A mutex serializes the two triggers.

`apply`:

1. `config.ParseAppConfig` inside a `recover`. The startup path is allowed to
   panic on a config it can't work with; a reload must only refuse it.
2. `startupConfig.RestartOnlyChanges(loaded)` — names the restart-only sections
   that differ and logs them as a warning.
3. `applier.ApplyUpstreams(loaded.UpstreamConfig.Upstreams)`.
4. Metrics and one log line with the diff.

`RestartOnlyChanges` compares each section by its YAML form, not with
`reflect.DeepEqual`. The running config carries state that is built while the
process works - `ScorePolicyConfig` caches its compiled score function on first
use - and a struct comparison reported that as a change on every reload (found
by the e2e test, section 11).

### chain-defaults, mode and failsafe settings on reload

- `chain-defaults.<chain>.poll-interval`, `chain-defaults.<chain>.options` and
  `mode` are inputs of the per-upstream defaults. They are folded into each
  upstream's config before the diff, so changing them **replaces the upstreams
  that inherit the changed value** and leaves the others alone. The warning
  about a restart-only section is still logged, because the same sections also
  hold settings that are not applied.
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

`validateUpstreams` repeats, against the *running process*, the checks whose
failure would otherwise surface only inside `CreateUpstream`:

- a non-empty list, no missing or duplicate ids, supported chains;
- a `rate-limit-budget` must exist in the budget registry that was built at
  startup. The file validation accepts a budget that is defined in the new
  file; `createRateLimiter` would then `log.Panic` on it;
- enough free upstream indices for the new ids.

A panic that still happens while an upstream added by a reload is being created
is recovered in `createAndStartUpstream` and reported as a failed start of that
upstream. At startup the same panic still stops the process, as before.

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
up.Stop()
b.forgetUpstream(up)          // dimensions + metric series
```

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
| `StickyRequestProcessor` | `GetUpstream` of a response's upstream is never nil | The response is returned without the index suffix |
| `GenericDimensionTracker` | Entries are never removed | `RemoveUpstream(chain, id)` drops the entries and the `nodecore_upstream_*` request and lag series |
| `RatingRegistry` | A rated upstream is rated forever | Each calculation drops the rating series of ids that are no longer in the chain |
| `UpstreamAutoTune` | Its gauge lives forever | The series is deleted when the upstream's context ends |
| Chain supervisor, block processors, websocket registry | Gauges per upstream are never deleted | `forgetUpstream` deletes `availability_status`, `blocks`, `heads`, `json_ws_*` for the removed upstream |

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
- **Requests in flight on a removed upstream.** HTTP-based connectors have no
  per-upstream connection state to close, so the request finishes on the
  caller's context. Websocket and gRPC connectors are closed; requests and
  subscriptions over them end, and the subscription engine reports the terminal
  error it reports for any lost source. Such a late request can re-create a few
  request-counter series for the removed upstream.
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
- **Reload before `StartUpstreams` ran.** `ApplyUpstreams` returns an error;
  only a `SIGHUP` in the first moments of the process can hit this.
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
  - an HTTP request in flight on a removed upstream completes.

Unit, elsewhere:

- `internal/reload`: a reload applies the list; broken files (not YAML, empty,
  cut in the middle of an upstream, unknown chain, duplicate id, no upstreams,
  invalid server setting) are rejected without calling the supervisor; recovery
  after a rejected file; the two-look rule; a rejected file is reported once;
  a missing file; the ticker-driven `Run`.
- `internal/config`: `watch-interval` default and minimum; two loads of the full
  example config show no restart-only change, also after the running config
  compiled its score function; the upstream list is not a restart-only change;
  changed sections are named.
- `internal/dimensions`, `internal/rating`: per-upstream cleanup, scoped to the
  chain.
- `internal/upstreams/flow`: an upstream that disappears after selection, and
  one that disappears while a sticky-create request is in flight.

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

The e2e test runs on `test/e2e/internal/mocknode`, a small JSON-RPC node added
with this change: it has no chain data behind it and needs no fork provider, so
the test needs no credentials, and it can serve a second chain id and answer one
method slowly, which a forked node cannot.

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

## 13. Key code references

- `internal/reload/config_reloader.go` — `ConfigReloader`: `Run`, `Reload`,
  `watch`, `apply`, the three metrics.
- `internal/config/config.go` — `ConfigPath`, `LoadAppConfig`, `ParseAppConfig`.
- `internal/config/reload_config.go` — `ReloadConfig`, `RestartOnlyChanges`.
- `internal/upstreams/upstream_reload.go` — `UpstreamsDiff`, `diffUpstreams`,
  `ApplyUpstreams`, `validateUpstreams`.
- `internal/upstreams/upstream_supervisor.go` — `managedUpstream`,
  `startUpstream`, `upstreamIndex`, `runUpstream`, `createAndStartUpstream`,
  `forwardUpstreamEvent`, `removeUpstream`, `waitUntilNotRouted`,
  `forgetUpstream`, `processEvents`.
- `internal/upstreams/chain_supervisor.go` — the existing `RemoveUpstreamEvent`
  handling this change relies on.
- `internal/dimensions/tracker.go` — `RemoveUpstream`.
- `internal/rating/registry.go` — `forgetGoneUpstreams`.
- `internal/upstreams/flow/request_processor.go`,
  `sticky_request_processor.go` — nil guards.
- `internal/app/app.go` — the reloader is created in `NewApp` and started in
  `Start`.
- `test/e2e/grpc/upstreams_reload_e2e_test.go`,
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
- **Draining a removed upstream.** Websocket and gRPC connectors are closed as
  soon as routing has dropped the upstream. A grace period for requests in
  flight would be the next step if it shows up in practice.
- **Reloading the routing settings** (`chain-defaults` dispatch and balancing,
  global `failsafe-config`, rate limit budgets). The reloader already detects
  these changes; the consumers would have to read them through something
  swappable.
- **Removing chain objects.** Would need a "chain removed" event for
  `SubscribeChainStatus` consumers; today the cost is one idle chain supervisor
  per chain that was ever configured.
- **Should the watch be on by default?** It is off to keep the behaviour of
  existing deployments, where editing the file had no effect until a restart.
- **Reload outcome on the health server.** `/status` could show the time and
  result of the last reload; today that is only in the metrics and the log.
