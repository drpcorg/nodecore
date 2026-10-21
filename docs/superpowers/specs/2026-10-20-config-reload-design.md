# Config reload

**Date:** 2026-10-20
**Branch:** `config-reload` (on top of main with PR #412 merged)
**Status:** design approved in discussion, ready for an implementation plan

## Problem

Every change to the upstream list needs a restart. dshackle reloads its upstream section on
SIGHUP (`ReloadConfigSetup`, `UpstreamConfigReloadConfigProcessor`); nodecore has nothing. The
feature is built on top of the lifecycle work in PR #412.

nodecore's config is harder to reload than dshackle's because the upstream section is not
independent of the rest of the file:

- An upstream's **effective config** is computed by `setDefaults` from `upstream-config.mode`
  (poll interval, head connector, integrity), from `chain-defaults` (`poll-interval`, `options`)
  and from `server.grpc-auth` (the `secure_signed` label).
- Upstreams are **validated** against the rate-limit budget names, the tor url, the chain specs,
  and the auth keys' allowed group-labels.

Several things needed for a removal do not exist today: nothing calls `GenericUpstream.Stop`,
the supervisor's upstream map only grows, and a stopped websocket connector leaves its pending
requests and subscriptions hanging (see §5).

## Goal and scope

Upstreams can be added, removed and changed in the config file and the change is applied
without a restart. Upstreams are identified by `id`:

- an id present in the file and not running is **added**;
- an id running and absent from the file is **removed**;
- an id present in both whose effective config differs is **changed**: the old instance is
  stopped and a new one started. There is no in-place update of any upstream setting.

Two triggers: `SIGHUP`, and an optional `watch-interval` that polls the file.

**Reloadable in v1:** `upstream-config.upstreams`, and the `chain-defaults` fields that are
baked into upstreams at defaults time (`poll-interval`, `options`). A change of those
chain-defaults shows up as "every upstream of that chain changed" and restarts them.

**Not reloadable in v1** (reported as "restart required", never applied):

- everything outside `upstream-config` (`server`, `cache`, `auth`, `rate-limit`,
  `app-storages`, `integration`, `stats`, `config-reload` itself);
- inside `upstream-config`: `mode`, `failsafe-config`, `score-policy-config`, `integrity`,
  `label-balancing`, `balancing-strategy`;
- the per-request fields of `chain-defaults`: `label-balancing`, `balancing-strategy`,
  `dispatch`, `local-subscriptions`, `validate-lag`. They are read per request from the startup
  `AppConfig` (or captured by the chain supervisor) and need a race-free config swap under the
  execution flow. Follow-up task.

Out of scope, deferred (see "Follow-ups"): deleting the metric series and dimension-tracker
entries of a removed upstream; stopping the chain supervisor of a chain with no upstreams left;
asynchronous `GenericUpstream.Start`.

## Design

### 1. Config

A new top-level section:

```yaml
config-reload:
  watch-interval: 30s   # optional; absent or 0 = reload on SIGHUP only
```

`AppConfig.ConfigReload *ConfigReloadConfig` (`yaml:"config-reload"`), defaulted to an empty
struct, validated as `watch-interval >= 0`. The section itself is restart-only.

`AppConfig` keeps the bytes it was parsed from, exposed as `Source() []byte`, set by
`NewAppConfig`. The reloader needs them as its baseline (§3).

### 2. The reloader (`internal/reload`)

```go
type UpstreamsApplier interface {
	ApplyUpstreams(confs []*config.Upstream) upstreams.UpstreamsDiff
}

func NewReloader(path string, startup *config.AppConfig, applier UpstreamsApplier) *Reloader
func (r *Reloader) Run(ctx context.Context, hups <-chan os.Signal)
```

`main.go` subscribes to `SIGHUP` with `signal.Notify` next to the existing INT/TERM
subscription, before `NewApp`, and hands the channel to the app; `App.Start` runs `Run` in a
goroutine. The config path comes from the same resolution `NewAppConfig` uses.

`Run` is a loop selecting on `hups`, on a ticker when `watch-interval > 0`, and on `ctx.Done()`.
Each trigger calls `reload(trigger)` in its own goroutine so the loop stays responsive.

`reload` does the same thing for both triggers:

1. **In-progress guard.** An `atomic.Bool` with `CompareAndSwap(false, true)`, cleared on
   return. If the swap fails, log `config reload is in progress, <trigger> is ignored` and
   return. No queueing, no mutex, no `TryLock` (dshackle semantics).
2. **Read and hash.** Read the file, `sha256` the bytes. If the hash equals the last handled
   one, log `config is unchanged, nothing to reload` when the last outcome was a success, or
   `config is unchanged since its last reload was rejected: <error>` when it was a rejection,
   and return. The hash is compared on signal reloads too; an operator who wants another
   attempt changes the file. These two lines, and the in-progress drop, are logged at
   Info/Warn for a signal (an operator asking) and at Debug for a watch tick (the reloader
   asking itself every interval), so an untouched file does not fill the log.
3. **Restart-only warnings.** `config.RestartOnlyChanges(startup.Source(), file)` (§3) and one
   warning per returned name: `'<section>' has changed in the config file but is only applied on
   restart`. These go out even when the reload is rejected later: they are facts about the file.
4. **Build and validate.** `config.LoadReloadable(startup.Source(), file)` (§3). On error the
   reload is rejected as a whole: log the error, record the hash with the error, return. The
   running upstreams are untouched.
5. **Apply.** `applier.ApplyUpstreams(merged.UpstreamConfig.Upstreams)`. Log the returned diff:
   `upstreams reloaded: added [..], removed [..], changed [..], failed [..]`, or `no upstream
   changes`. A non-empty `Failed` (an upstream the supervisor could not build from a config
   that passed validation, e.g. a CA file not mounted yet) makes the reload *incomplete*: it
   is logged as an error and the hash is **not** recorded, so the next trigger applies the
   same content again. Retrying is free because the old instance of a changed id was kept.
6. Otherwise record the hash as handled with a nil error.

Content hashing rather than mtime: Kubernetes config maps update through symlink swaps and
editors write temp files; only the bytes are trustworthy. A reload reads the file in one
`os.ReadFile`; a torn write that parses is accepted like any other content, the next change
corrects it.

The startup `AppConfig` is the baseline for the whole life of the process and is never
mutated. The reloader holds no other config state.

### 3. Config package: building the config a reload may apply

Two exported functions in `internal/config/reload.go`, and `NewAppConfig` split so both can reuse
`parseAppConfig(file []byte) (*AppConfig, error)` (plain `yaml.Unmarshal`, no defaults).

```go
// LoadReloadable builds the config a reload may apply: the startup file with only the
// reloadable parts taken from file. Everything else keeps its startup value, so the
// upstreams are defaulted under the running mode and grpc-auth and validated against the
// running budgets, tor url and auth keys.
func LoadReloadable(startup, file []byte) (*AppConfig, error) {
	merged, err := parseAppConfig(startup)
	...
	fresh, err := parseAppConfig(file)
	...
	takeReloadable(merged, fresh)
	merged.setDefaults()
	return merged, merged.validate()
}

// takeReloadable is the one place that says what a reload can change.
func takeReloadable(dst, src *AppConfig) {
	// nil-safe on both UpstreamConfig pointers
	dst.UpstreamConfig.Upstreams = src.UpstreamConfig.Upstreams
	dst.UpstreamConfig.ChainDefaults = src.UpstreamConfig.ChainDefaults
}
```

The merged object is built from bytes every time: nothing is shared with the live config, no
cloning, and `setDefaults` idempotency is not relied on.

Why merge rather than validate the new file as a whole and take its upstreams: the file could
define a budget and reference it, pass validation, then panic in `createRateLimiter` because
the running budget registry never got it; or change `mode` and recompute every upstream's
defaults under a mode the running executor and integrity do not use, producing a spurious
"every upstream changed" diff. With the merge the only thing that varies in the validated
config is what will actually be applied. The "at least one upstream" rule and the auth
group-label rule therefore reject a file as a whole, applying nothing.

`chain-defaults` is taken whole because only `poll-interval` and `options` feed into upstream
defaults; its per-request fields in the merged object are never read (the execution flow reads
the startup `AppConfig`).

```go
// RestartOnlyChanges names the top-level sections (and upstream-config sub-sections) in
// which file differs from startup, apart from what a reload applies. Sections are not
// listed anywhere: every field of AppConfig that takeReloadable does not touch is
// restart-only by definition, so a new section is covered without touching this code.
func RestartOnlyChanges(startup, file []byte) ([]string, error)
```

Both files are parsed and fully defaulted on their own (so filled-in defaults never show up as
differences), `takeReloadable(base, fresh)` cancels out the reloadable fields, then a
reflection walk over `AppConfig`'s fields compares each with `reflect.DeepEqual` and reports
the yaml tag of each field that differs. It descends one level into `*UpstreamConfig` so a mode
change is reported as `upstream-config.mode`, not `upstream-config`. Two refinements:

- `ScorePolicyConfig` carries the unexported compiled-function cache; it is compared through a
  view of its three public fields.
- The per-request `chain-defaults` fields are the exception inside a reloadable section: a small
  per-chain comparison of `label-balancing`, `balancing-strategy`, `dispatch`,
  `local-subscriptions`, `validate-lag` reports `chain-defaults.<chain>.<field>`. This list goes
  away when the follow-up makes them live.

### 4. Upstream supervisor: `ApplyUpstreams`

```go
// interfaces.go
type UpstreamsDiff struct{ Added, Removed, Changed []string }
func (d UpstreamsDiff) IsEmpty() bool

type UpstreamSupervisor interface {
	...
	StartUpstreams()
	// ApplyUpstreams reconciles the running upstreams with confs: starts the new ones,
	// removes the missing ones and replaces the changed ones. confs are effective
	// (post-defaults, validated) upstream configs.
	ApplyUpstreams(confs []*config.Upstream) UpstreamsDiff
}
```

The supervisor's state becomes a map `id -> runningUpstream{conf *config.Upstream, upstream
Upstream, stop chan struct{}, done chan struct{}}` guarded by `applyMu`, replacing the
loose `upstreams` CMap for bookkeeping (`GetUpstream` keeps serving from a CMap or the same map
under a read lock). `StartUpstreams` starts `processEvents` and calls
`ApplyUpstreams(initialConfigs)` on the empty map, so startup and reload share one code path.
`ApplyUpstreams` holds `applyMu` for its whole duration; startup and a reload serialize.

**Diff.** `wanted` keyed by id. For each running id: absent from `wanted` → removed; present
with `!reflect.DeepEqual(running.conf, want)` → removed and added (changed). Each wanted id not
running → added. `DeepEqual` on effective `*config.Upstream` is sound: every field is plain data
(strings, durations, `*bool`, maps, slices), both sides come out of the same defaults pipeline so
nil-vs-empty pointers normalize identically. Slice order matters: reordering connectors or
method lists counts as a change. A chain change under the same id is a change like any other.
An id whose `CreateUpstream` failed is reported in `UpstreamsDiff.Failed` and not stored; the
reloader treats that as an incomplete reload (see §2). An id whose settings validation was
fatal stays stored and not running, as today, until its config changes.

**Create, then remove, then start.** The instances to add are created first, in parallel
and waited for: creation is local work (connectors, processors, method set; nothing dialled or
started), so it is safe while the old instance of a changed id is still running. A replacement
that cannot be built leaves the old instance running on its old settings; the id goes to
`Failed` instead of `Changed`. Then all removals run in parallel and are waited for, and only
then are the created instances stored and started (their start and health stay asynchronous),
so the new instance of a changed id can never race the old one's events through the chain
supervisor.

**One removal:**

1. Close `stop`. The forwarder goroutine (the one copying the upstream's events into the
   supervisor's `eventsChan`) selects on it, pushes a synthesized `RemoveUpstreamEvent{Id,
   Chain}` as its last event, unsubscribes, closes `done` and exits. Wait for `done` with a
   30 s timeout and a warning. From here the chain supervisor drops the upstream from selection
   and no later event of the old instance can reach it: the forwarder exit *is* the
   deregistration. Events the still-running upstream publishes afterwards have no subscriber
   and are dropped by the subscription manager.
2. Sleep the grace period, `removalGracePeriod = 5 * time.Second` (constant, not configurable),
   so unary requests already on the upstream can finish. `GetUpstream` still resolves the id
   during the grace.
3. `upstream.Stop()`. It may block up to the settings-validation bound if the instance is still
   validating (current `GenericUpstream` lifecycle logic, unchanged by decision); removals run
   in parallel so this does not add up.
4. Delete the id from the map.

Ordering guarantees rest on the existing single `eventsChan` consumed by one `processEvents`:
the Remove is enqueued before the forwarder exits, additions are enqueued after all removals
finished, so for a changed id Remove(old) precedes every event of new. The chain supervisor's
`RemoveUpstreamEvent` handler already ignores an id it has no state for (an instance that never
got past validation). `processEvents`'s `PartialStop` on Remove is harmless for an instance
about to be stopped.

**Indices.** A new instance takes the next value of `upstreamIndicesCounter`; the counter is
never reused, so sticky filter ids minted by a replaced instance fail instead of being routed to
a node that does not know them. The `0xfffff` cap stays.

**Nil lookups.** `GetUpstream` now returns nil for an id that was selected a moment ago. Every
call site in `internal/upstreams/flow` that dereferences the result is audited and guarded
(`request_processor.go`, `selectors.go`, `fanout_request_processor.go`, `method_hook.go`,
`integrity_request_processor.go`, `execution_flow.go`, `pending_tx_source.go`,
`label_group_strategy.go`, `not_null_request_processor.go`, `sticky_request_processor.go`;
`sub_aggregation.go` already is). A missing upstream is treated as "no available upstreams" for
that attempt; in the sticky processor a response whose upstream is gone is turned into that
error rather than minting a filter id without an index.

**Chain with no upstreams left.** Its chain supervisor stays in the map, status Unavailable,
head feeds and state subscriptions stay open and go quiet, requests get "no available
upstreams", an upstream added back later registers with the same supervisor. No chain-removed
event.

### 5. Websocket teardown fixes (`internal/upstreams/ws`)

Found while tracing removal; both are prerequisites, since `Stop` has had no production caller
until now.

- **Request registry cleans up on context end.** `GenericUpstream`'s teardown cancels the
  upstream context first; the registry's `run` loop exits on that context, so the `cancelAll`
  command the ws loop sends afterwards is dropped by `sendCmd`, and every pending ws request and
  node subscription on the upstream is left hanging (unary ones die by their one-minute timeout,
  subscriptions never). Fix: the `ctx.Done()` branch of `run` cancels every operation itself,
  the way `cancelAllCommand.handle` does: `SetSkipDoOnClose` (no unsubscribe writes to a socket
  that is closing), then `Cancel`. It iterates `allOps`, not `registryState`: `Register` stores
  into `allOps` before its command is handled, so register commands still queued when the context
  fires are covered too; `Cancel` is a compare-and-swap, so a double cancel is a no-op. With
  this, the teardown order in `GenericUpstream` does not matter and is left as is. Side effect:
  on app shutdown ws operations no longer leak.
- **`WsDisconnected` on loop exit.** The ws loop's `ctx.Done()` paths call `disconnect` but do
  not publish `WsDisconnected`, so the aggregation engine's sources on that connector never get
  their terminal frame and client subscriptions go silent. Fix: publish `WsDisconnected` after
  `disconnect` on those paths. Consumers are idempotent (sources return on the first one and
  unsubscribe; the cap detector dropping `WsCap` twice yields the same state, deduped by
  `Same`), the publish may land after `Stop` returns (tests wait on the event, not on `Stop`),
  and replay subscribers correctly see `Disconnected` afterwards.

Connector `Stop` otherwise stays as is: HTTP is a no-op (in-flight requests complete on the
caller's context), gRPC closes the client conn, ws cancels the loop. The grace period is what
protects in-flight ws and gRPC calls; anything still running after it fails and the retry policy
decides.

### 6. Documentation

`docs/nodecore/01-config.md`: the `config-reload` section, the two triggers, what is reloadable
and what is restart-only with the warning text, the id rule (changed = stop and start), the
5 s grace, and that a rejected file is reported once until it changes.

## Testing

- **config:** `LoadReloadable` keeps every restart-only section from `startup` (server port,
  budgets, mode) while taking upstreams and chain-defaults from `file`; upstreams are defaulted
  under the startup mode and chain-defaults poll-interval; a new file referencing a budget the
  startup file does not define is rejected; a new file that removes the last upstream or a
  group-label an auth key needs is rejected. `RestartOnlyChanges` reports `server`,
  `upstream-config.mode`, `chain-defaults.ethereum.dispatch`, and nothing for a file that only
  changes upstreams or chain-defaults poll-interval; a defaults-only difference (an omitted
  field) reports nothing.
- **reload:** unchanged hash skips with the right log; a rejected file is handled once and the
  second trigger reports the stored error; a trigger during a running reload is dropped; the
  ticker path and the signal path call the applier with the same list; the applier is not called
  on a rejected file.
- **upstreams:** `ApplyUpstreams` on an empty map adds all; removing an id pushes exactly one
  `RemoveUpstreamEvent` to the chain supervisor, and no event of the removed instance reaches the
  chain supervisor after it; a changed id yields Remove(old) before any event of new; two
  removals in one apply overlap (total time ≈ one grace, with the grace constant made small for
  tests); an unchanged id is not touched (same instance pointer); `GetUpstream` resolves the id
  during the grace and not after; a failed `CreateUpstream` is reported as failed and tried
  again on the next apply; a replacement that fails to build leaves the old instance running.
  Flow call sites: a nil `GetUpstream` yields "no available upstreams" instead of a panic
  (sticky processor included).
- **ws:** registry context end cancels every operation including one whose register command
  was still queued, with `ShouldDoOnClose() == false`; the processor publishes `WsDisconnected`
  when stopped while connected.

## Tasks

Separable, in this order:

1. ws teardown fixes (§5) with tests.
2. Supervisor `ApplyUpstreams`, removal sequence, `StartUpstreams` on top of it, flow nil-guard
   audit (§4) with tests.
3. Config: `config-reload` section, `Source()`, `parseAppConfig` split, `LoadReloadable`,
   `RestartOnlyChanges` (§1, §3) with tests.
4. Reloader package, `main.go`/`app.go` wiring, docs (§2, §6) with tests.

## Follow-ups (deferred, in the followups list)

- Live reload of the per-request chain-defaults and top-level `label-balancing` /
  `balancing-strategy` (race-free swap under the execution flow).
- Metrics and dimensions cleanup for removed upstreams: series in chain supervisor, rating,
  dimensions, block/head gauges, ws registry, rate-limit auto-tune, plus the dimension tracker's
  in-memory entries. Bounded by the number of distinct ids ever configured, so tolerated in v1.
- Stopping and deleting the chain supervisor of a chain with no upstreams left.
- Asynchronous `GenericUpstream.Start` (already listed).
