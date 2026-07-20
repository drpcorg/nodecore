# Upstream config guide

This `upstream-config` section defines how nodecore discovers, evaluates, and interacts with upstream blockchain providers.

```yaml
upstream-config:
  mode: default
  integrity:
    enabled: true
  failsafe-config:
    retry:
      attempts: 10
      delay: 2s
      max-delay: 5s
      jitter: 3s
    hedge:
      delay: 500ms
      max: 2
  chain-defaults:
    ethereum:
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-validation: false
        disable-settings-validation: false
        disable-chain-validation: false
        disable-health-validation: false
        disable-lower-bounds-detection: false
        disable-safe-block-detection: false
        disable-finalized-block-detection: false
        disable-labels-detection: false
        validate-syncing: true
        validate-peers: true
        min-peers: 5
        validate-call-limit: true
        call-limit-size: 131072
        validate-client-version: false
        disable-log-index-validation: true
        archive: false
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
      poll-interval: 45s
    polygon:
      poll-interval: 30s
  score-policy-config:
    calculation-interval: 5s
    calculation-function-name: "defaultLatencyErrorRatePolicyFunc"
    #calculation-function-file-path: "path/to/func"
  upstreams:
    - id: my-super-upstream
      chain: ethereum
      rate-limit-budget: standard-budget
      connectors:
        - type: json-rpc
          url: https://path-to-eth-provider.com
          headers:
            my-header: my-header-value
          response-header-deny:
            - X-Provider-Name
    - id: full-upstream
      chain: polygon
      connectors:
        - type: json-rpc
          url: https://path-to-polygon-provider.com
          headers:
            my-header: my-header-value
        - type: websocket
          url: wss://path-to-polygon-provider.com
      head-connector: websocket
      poll-interval: 5s
      options:
        internal-timeout: 15s
        validation-interval: 10s
        disable-settings-validation: false
        disable-chain-validation: true
      methods:
        ban-duration: 10m
        enable:
          - "my_method"
        disable:
          - "eth_getBlockByNumber"
      rate-limit:
        rules:
          - method: eth_getBlockByNumber
            requests: 100
            period: 1s
          - pattern: trace_.*
            requests: 5
            period: 2m
      failsafe-config:
        retry:
          attempts: 10
          delay: 2s
          max-delay: 5s
          jitter: 3s
```

It brings together:

1. Mode (`mode`) - Picks the overall operating profile. `default` is the cost-conscious profile: heads are polled lazily over HTTP, most periodic validators are off, and the [integrity](#integrity) feature is the recommended opt-in to compensate for stale reads. `strict` is the high-fidelity profile: heads are tracked over WebSocket when configured (or polled at the chain's block time when only HTTP is available), every validator runs, and `integrity` is forcibly off because real-time tracking makes it redundant. See [mode](#mode) below.
2. Integrity (`integrity`) - Guarantees that methods like `eth_blockNumber` and `eth_getBlockByNumber` never return stale data. When enabled, nodecore validates responses against the current head and retries with the highest-synced upstream if needed.
3. Failsafe configuration (`failsafe-config`) - Global resilience settings: retries (attempts, backoff, max delay, jitter), hedging (duplicate a slow request after a delay, with a cap on parallel hedges), and a per-request `timeout` budget.
4. Chain defaults (`chain-defaults`) - Per-chain operational defaults: poll interval, validation toggles, and detector toggles. See [Validators and labels](#validators-and-labels) below.
5. Scoring policy (`score-policy-config`) - Controls how upstream health/quality is calculated: a calculation interval and a scoring function. The score blends metrics like latency and error rate and is used by the router to pick the best upstream.
6. Upstreams (`upstreams`) - The actual provider entries.

Together, these settings let you (1) register providers, (2) tune resiliency and polling, (3) define how nodecore scores and selects the best upstream at runtime, (4) apply rate limiting to control request throughput, and (5) toggle the validators and label detectors that observe each upstream's health.

## mode

```yaml
upstream-config:
  mode: default
```

`mode` chooses the overall operating profile for every upstream. It is more than a connector preference - it also shifts the default values of several `chain-defaults.options.*` flags and the default `poll-interval`. Anything you set explicitly under `chain-defaults` or on an individual upstream still wins; `mode` only changes what unset fields *fall back to*.

### `mode: default` (default)

The cost-conscious profile. nodecore deliberately runs lazy and minimises the amount of work it does against each upstream:

- **Head tracking via HTTP polling at `1m`.** When `head-connector` is not set, nodecore picks the *simplest* connector available (`json-rpc` over `rest` over `grpc` over `websocket`). For an upstream with both `json-rpc` and `websocket` connectors, the WebSocket is left dormant and heads are pulled by polling JSON-RPC.
- **Most periodic validators are off by default.** With no explicit overrides, the following fall back to *disabled*: `disable-lower-bounds-detection`, `disable-labels-detection`, `validate-syncing`, `validate-peers`, `validate-call-limit`. Only the cheap, structural validators run (chain id / net version, health).
- **Stale-read protection is opt-in.** Because heads are polled lazily, `eth_blockNumber` and `eth_getBlockByNumber` can return values older than the actual tip. To compensate, enable the [integrity](#integrity) feature (`integrity.enabled: true`) - it cross-checks responses against the most-advanced upstream and retries when they look stale. Integrity is the recommended way to get consistency in `default` mode.

This mode is the right choice when upstreams are expensive (paid RPCs, metered providers) and you care more about cost / request-count than about being on the exact-current head.

### `mode: strict`

The high-fidelity profile. nodecore tracks upstream state as aggressively as it can:

- **Head tracking via WebSocket when available.** When `head-connector` is not set, nodecore picks the *most capable* connector (`websocket` over `grpc` over `rest` over `json-rpc`). If a WebSocket connector is configured, heads come from `newHeads`-style subscriptions in real time. If the upstream only exposes HTTP, the fallback `poll-interval` is the chain's `expected-block-time` from [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml) rather than `1m`, so even HTTP-only upstreams stay close to the tip.
- **All validators run by default.** With no explicit overrides, the following fall back to *enabled*: lower-bounds detection, labels detection, `eth_syncing`, `net_peerCount` (with the `min-peers` threshold), and the `eth_call` return-data limit probe. This produces an accurate, real-time view of each upstream's capabilities for routing decisions.
- **[Integrity](#integrity) is forcibly disabled.** Real-time head tracking (or block-time polling) makes the lazy stale-read compensation unnecessary. Setting `integrity.enabled: true` together with `mode: strict` is accepted by the parser but logged as a warning, and the feature is turned off.

This mode is the right choice when upstreams are self-hosted or unmetered, when you need accurate label-based routing (e.g. archive vs. full nodes), or when always-current data matters more than minimising upstream traffic.

### Per-mode defaults at a glance

| Field | `default` mode fallback | `strict` mode fallback |
|---|---|---|
| `head-connector` (when unset) | simplest connector type | most capable connector type |
| `poll-interval` (when unset) | `1m` | chain's `expected-block-time` |
| `disable-lower-bounds-detection` | `true` (off) | `false` (on) |
| `disable-labels-detection` | `true` (off) | `false` (on) |
| `validate-syncing` | `false` (off) | `true` (on) |
| `validate-peers` | `false` (off) | `true` (on) |
| `validate-call-limit` | `false` (off) | `true` (on) |
| `integrity.enabled` | as configured (default `false`) | forced to `false` |
| `disable-log-index-validation` | `true` (off) | `false` (on) |
| `validate-client-version` | `false` (off) | `true` (on) |
| `chain-defaults.<chain>.dispatch.*` | `false` (off) | `true` (on) |

## Extending the chain registry at startup

NodeCore ships with the public chain registry embedded from
[`drpcorg/public`](https://github.com/drpcorg/public). For private or
consortium chains (e.g. a Besu network with a customer-specific chain id),
two environment variables let you extend the registry at startup without
forking:

| Env var | Purpose |
|---------|---------|
| `NODECORE_EXTRA_CHAINS_PATH` | Path to a YAML file using the same schema as `chains.yaml`. Its entries are merged on top of the embedded registry. |
| `NODECORE_SPECS_PATH` | Directory of JSON method-spec files (same schema as `pkg/methods/specs/*.json`). The specs found here **extend** the embedded specs — they are loaded *in addition to* the built-in ones. A spec whose `name` already exists in the embedded set is rejected, so extras can only add new method specs, not silently replace built-ins. |

A minimal extra-chains file for a single Besu network:

```yaml
chain-settings:
  default:
    expected-block-time: 2s
    lags:
      lagging: 5
      syncing: 10
  protocols:
    - type: eth
      settings:
        method-spec: "eth"
      chains:
        - id: BesuPrivate
          short-names: [besu-acme-prod]
          chain-id: "0xdeadbeef"
          grpcId: 60001
```

Then point an upstream at it like any other chain:

```yaml
upstreams:
  - id: besu-acme
    chain: besu-acme-prod
    connectors:
      - type: json-rpc
        url: http://besu.internal:8545
```

NodeCore will:

- Allocate an internal `Chain` id for any extra short-name that isn't
  already registered (ids start at 2³⁰ to avoid colliding with generated
  values).
- Return the correct `eth_chainId` and `net_version` for the extra chain
  because both methods read from the configured `chain-id` rather than
  forwarding upstream.
- Reject duplicate short-names or `grpcId` collisions with already-known
  chains at startup.

## integrity

```yaml
integrity:
  enabled: true
```

By default, NodeCore polls upstreams periodically and does not maintain real-time tracking of chain heads. This can lead to situations where certain methods, such as `eth_blockNumber` or `eth_getBlockByNumber`, return stale values. The integrity feature ensures that these methods always return values that are consistent with or ahead of the currently known head.

When `integrity.enabled`: `true`, NodeCore enforces the following guarantees:

1. Non-decreasing results. Returned block numbers are always greater than or equal to the previously observed head or finalized block. NodeCore will never return an older block.

2. Validation and fallback. When a response from an upstream is less than the current head or finalized block, NodeCore automatically retries the request against the upstream with the highest known head (upstreams are pre-sorted by block height).

3. Manual head updates. When a response is greater than the currently tracked head or finalized block, NodeCore updates its internal head/finalized state immediately to reflect the newer value.

This mechanism provides stronger consistency guarantees without requiring full real-time head tracking across all upstreams.

## failsafe-config

```yaml
failsafe-config:
  retry:
    attempts: 10
    delay: 2s
    max-delay: 5s
    jitter: 3s
  hedge:
    delay: 500ms
    max: 2
```

`failsafe-config` defines global resilience rules that the execution flow uses while handling a request across multiple upstreams. The execution flow picks the current best upstream as provided by the scoring subsystem and applies hedging for slowness and retries for retryable errors, potentially switching to a different upstream on subsequent attempts.

Execution scheme:

1. Pick an upstream.
2. Send a request
   - If `hedge.delay` elapses with no response, the execution flow may launch a hedge (speculative parallel request), up to `hedge.max` additional copies. Hedges can target other upstreams.
3. First success wins. As soon as any in-flight attempt (original or hedge) returns a successful response, the executor returns it and cancels other in-flight attempts.
4. Retry on retryable errors. If an attempt returns a retryable error, the execution flow applies the retry policy. Non-retryable errors end the flow immediately (returned to the client).

nodecore uses the [failsafe-go](https://failsafe-go.dev/) library for resiliency primitives. At the moment we rely on:

- Retry policy – provided by failsafe-go
- Hedge policy – [custom implementation](../../internal/resilience/parallel_hedge.go) (instead of the default) for stricter latency semantics

**Why a custom hedge policy?**

The baseline hedging behavior has a few drawbacks for our use case:

- If an immediate error arrives from the primary request, the default behavior may still wait for the hedge delay (or cascade hedges serially), slowing error paths.
- Hedge requests may be issued sequentially, effectively waiting for each attempt instead of firing truly in parallel after the delay.

To eliminate these issues, nodecore implements a pure hedging strategy with clear timing and parallelism guarantees:

- Delay gate - A hedged request is sent only if `hedge.delay` has fully elapsed and the primary has not completed successfully. If we receive any response (success or error) before `hedge.delay` elapses, we return it immediately (no hedges launched)
- Parallel launch - Once `hedge.delay` elapses with no success, we launch up to `hedge.max` parallel hedges immediately (not one-by-one)
- First-success-wins - As soon as any in-flight attempt (primary or any hedge) returns success, we return that response and cancel all other in-flight attempts.

`failsafe-config` fields:

1. The `retry` section:
   - `attempts` - Maximum number of request attempts, including the initial one. **_Default_**: `3`
   - `delay` - Base wait time before a retry. Used as the starting backoff.
   - `max-delay` - Upper bound for retry backoff. The effective delay will never exceed this value
   - `jitter` - Adds randomization to each backoff
2. The `hedge` section:
   - `delay` - How long to wait after sending the initial request before launching hedged requests. Can't be less than 50ms. **_Default_**: `1s`
   - `max` - Maximum number of additional parallel hedged requests to launch once the delay has elapsed. **_Default_**: `2`

## chain-defaults

```yaml
chain-defaults:
  ethereum:
    options:
      internal-timeout: 5s
      validation-interval: 30s
      disable-validation: false
      disable-settings-validation: false
      disable-chain-validation: false
      disable-health-validation: false
      disable-lower-bounds-detection: false
      disable-labels-detection: false
      validate-syncing: true
      validate-peers: true
      min-peers: 5
      validate-call-limit: true
      call-limit-size: 131072
    poll-interval: 45s
    dispatch:
      broadcast: true
      maximum-value: true
      not-null: true
    local-subscriptions:
      enable: true
      enable-new-heads: true
      enable-logs: true
      enable-new-pending-transactions: true
  polygon:
    poll-interval: 30s
```

The `chain-defaults` section defines per-chain baseline settings. `<chain>.options` apply to upstreams of that chain unless explicitly overridden in the upstream configuration; `<chain>.dispatch` controls routing policies for the whole chain and is not a per-upstream setting.

`chain-defaults` fields:

* `<chain>.options` - Behavioral, validation, and detector toggles for all upstreams of this chain. Several of the boolean toggles fall back to different values depending on [`mode`](#mode); see the per-mode defaults table there for the exact fallbacks. Leaving a `*bool` field unset means "use the mode-dependent fallback":
  * `internal-timeout` - Maximum time allowed for internal nodecore probes (head poll, settings validators, label detectors). **_Default_**: `5s`
  * `validation-interval` - How frequently nodecore re-runs validators and label detectors against the upstream. **_Default_**: `30s`
  * `disable-validation` - Master switch. When `true`, no validators of any kind run. **_Default_**: `false`
  * `disable-settings-validation` - Disables the settings validators as a group (chain id / net version, peers, syncing, call-limit). **_Default_**: `false`
  * `disable-chain-validation` - Disables only the chain-id / net-version validator. **_Default_**: `false`
  * `disable-health-validation` - Disables only the health validators (per chain family). **_Default_**: `false`
  * `disable-lower-bounds-detection` - Disables the earliest-available-block detector. Mode-dependent default: `true` in `default` mode, `false` in `strict` mode
  * `disable-labels-detection` - Disables the EVM label detectors (client/version, archive, gas, flashblock, etc.). Mode-dependent default: `true` in `default` mode, `false` in `strict` mode
  * `validate-syncing` - For EVM chains, calls `eth_syncing` periodically and marks the upstream unavailable when it is syncing. For beacon-chain upstreams it probes `GET /eth/v1/node/syncing` instead (marking the upstream `Syncing`, or `Unavailable` when its execution layer is offline). For NEAR upstreams the `status` probe's `sync_info.syncing` and a stale-head guard on `latest_block_time` drive the same signal (always on as part of health validation). Ripple upstream health derives from `server_state` (`full`/`validating`/`proposing`/`tracking` = healthy, `connected`/`syncing` = syncing, `amendment_blocked` = down). Starknet upstreams probe `starknet_syncing` (a plain `false` or a sync object, judged with a lag threshold). TON upstream health uses `getMasterchainInfo` liveness (v2) and `masterchainInfo` `gen_utime` freshness (v3). Mode-dependent default: `false` in `default` mode, `true` in `strict` mode
  * `validate-peers` - For EVM chains, calls `net_peerCount` periodically and pairs with `min-peers`. For beacon-chain upstreams it probes `GET /eth/v1/node/peer_count`. NEAR upstreams probe `network_info` `num_active_peers`. Ripple upstreams read `peers` from the same `server_state` payload. Starknet has no peer probe (nodes sync from the feeder gateway, not p2p), so `validate-peers` has no effect there; likewise for TON upstreams. Mode-dependent default: `false` in `default` mode, `true` in `strict` mode
  * `min-peers` - Minimum acceptable peer count when `validate-peers` is on. **_Default_**: `1`
  * `validate-call-limit` - For EVM chains, periodically probes the upstream's `eth_call` return-data limit and marks the upstream unhealthy when its observed limit is below `call-limit-size`. Mode-dependent default: `false` in `default` mode, `true` in `strict` mode
  * `call-limit-size` - Threshold (in bytes) of the smallest acceptable `eth_call` return-data limit. **_Default_**: `1000000` (1 MB)
  * `validate-client-version` - For EVM chains, validates the detected `web3_clientVersion`/client labels against the embedded compatible-client rules. Mode-dependent default: `false` in `default` mode, `true` in `strict` mode
  * `disable-log-index-validation` - Disables the EVM receipt log-index validator. The validator detects upstreams whose `logIndex` resets per transaction instead of increasing globally through the block. Mode-dependent default: `true` in `default` mode, `false` in `strict` mode
  * `disable-safe-block-detection` - Disables periodic safe-block polling on EVM upstreams. When `true`, nodecore skips `eth_getBlockByNumber("safe", …)` calls. **_Default_**: mode-dependent — `true` in `default` mode, `false` in `strict` mode
  * `disable-finalized-block-detection` - Disables periodic finalized-block polling on EVM upstreams. When `true`, nodecore skips `eth_getBlockByNumber("finalized", …)` calls, does not cache with `finalization-type: finalized`, and skips finalization-lag tracking. Set to `true` for chains like Viction (PoSV) that lack Ethereum's finalized-block concept. **_Default_**: `false`
  * `archive` - Manual EVM archive capability override. Set `archive: false` to publish `archive=false` without running archive auto-detection. Set `archive: true` or leave it unset to use the runtime archive detector and publish its detected result
* `<chain>.dispatch` - Per-chain dispatch policy toggles. These options affect routing for the whole chain, not individual upstreams:
  * `broadcast` - Enables fan-out broadcast for method specs with `dispatch: broadcast` (for example transaction propagation). In `default` mode this falls back to `false`; in `strict` mode it falls back to `true`.
  * `maximum-value` - Enables fan-out maximum-value aggregation for method specs with `dispatch: maximum-value` (for example nonce-like reads). In `default` mode this falls back to `false`; in `strict` mode it falls back to `true`.
  * `not-null` - Enables sequential retry for method specs with `dispatch: not-null`. In `default` mode this falls back to `false` to avoid extra upstream requests; in `strict` mode it falls back to `true`. See [Method specs](11-method-specs.md#settings) for dispatch semantics
* `<chain>.poll-interval` - How often nodecore polls upstreams of that chain for new head / finality information
  * Example: `ethereum.poll-interval: 45s` means all Ethereum upstreams are polled every 45 seconds unless overridden. The **_default_** is `1m` in `mode: default`, and the chain's expected block time in `mode: strict`
* `<chain>.label-balancing` - Per-chain override of the global [label-balancing](#label-balancing) block. When set it fully replaces the global block for this chain
* `<chain>.local-subscriptions` - Per-chain control over locally-synthesized subscriptions. For `eth_subscribe` topics that nodecore can serve by aggregating across upstreams (`newHeads`, `logs`, `newPendingTransactions`), this decides whether to use that local source or fall back to a plain node-backed passthrough (a single upstream subscription). All toggles default to `true` (local synthesis on, the historical behavior) and only apply where the chain actually has the capability:
  * `enable` - Master switch for the chain. `enable: false` turns off local synthesis for all three topics
  * `enable-new-heads` / `enable-logs` / `enable-new-pending-transactions` - Per-topic overrides that win over `enable` (e.g. `enable: false` with `enable-logs: true` keeps only `logs` local)
  * Note: the synthetic `drpc_pendingTransactions` method has no node-backed equivalent and is **always** served locally — it is never affected by these flags
  * See [Subscriptions](13-subscriptions.md) for how local synthesis and aggregation work

> **⚠️ Note**: Chain names in this section must match the identifiers defined in [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml)

See [Validators and labels](#validators-and-labels) below for what each validator does and how it maps to these flags.

`gas-price-condition` is a chain metadata setting from embedded `chains.yaml`, not a per-upstream option. When present for an EVM chain, nodecore validates `eth_gasPrice` against the configured comparison conditions (for example `eq`, `ne`, `gt`, `gte`, `lt`, `lte` or symbolic equivalents) as part of settings validation.

## score-policy-config

```yaml
score-policy-config:
  calculation-interval: 5s
  calculation-function-name: "defaultLatencyErrorRatePolicyFunc"
  #calculation-function-file-path: "path/to/func"
```

The `score-policy-config` section defines how nodecore evaluates and ranks upstreams. It provides a flexible rating subsystem that uses built-in or user-defined Typescript functions to compute scores based on multiple performance dimensions. The result of this calculation directly influences which upstream is selected by the execution flow.

**How it works**:

1. Metrics collection. or each chain and RPC method, nodecore continuously tracks:
   - Latency percentiles: p90, p95, p99
   - Request statistics: total requests, total errors, error rate, successful retries
   - Blockchain state metrics: head lag (distance from the latest head), finalization lag (distance from the latest finalized block)
2. Rating subsystem. At each `calculation-interval`, the rating subsystem invokes a scoring function that calculates the upstreams' rating based on these metrics.
   - By default, a built-in function (e.g. defaultLatencyErrorRatePolicyFunc) is used. [All default functions](../../internal/config/default_ts_funcs.go)
   - Optionally, you can provide a custom TypeScript function that defines your own rating logic
3. Execution flow - the execution flow itself does not evaluate upstreams; it simply picks the best one according to the latest rating.

**Writing a custom scoring function with the following rules**:

1. Function signature

```typescript
function sortUpstreams(upstreamData: UpstreamData[]): SortResponse;
```

2. `UpstreamData` object

```typescript
{
  id: string,
  method: string,
  metrics: {
    latencyP90: number,
    latencyP95: number,
    latencyP99: number,
    totalRequests: number,
    totalErrors: number,
    errorRate: number,
    headLag: number,
    finalizationLag: number,
    successfulRetries: number
  }
}
```

3. `SortResponse`

```typescript
{
  sortedUpstreams: string[],  // list of upstream IDs sorted from best → worst
  scores: {
    id: string,               // upstream identifier
    score: number             // calculated score for this upstream
  }[]
}
```

`score-policy-config` fields:

- `calculation-interval` - How often the scoring subsystem recalculates upstream scores. **_Defaults_**: `10s`
- `calculation-function-name` - The name of a built-in scoring function to use. Possible functions - `defaultLatencyPolicyFunc`, `defaultLatencyErrorRatePolicyFunc`. **_Default_**: `DefaultLatencyPolicyFuncName`
- `calculation-function-file-path` - Path to a custom TypeScript file implementing your own scoring function

> **⚠️ Note**: Both `calculation-function-name` and `calculation-function-file-path` can't be set at the same time.

## label-balancing

```yaml
upstream-config:
  # global default — applies to every chain unless overridden under chain-defaults
  label-balancing:
    order:
      - full
      - archive
      - fast
    pass-on-error: false
    include-default: true
  chain-defaults:
    polygon:
      # per-chain override — fully replaces the global block for this chain
      label-balancing:
        order:
          - archive
          - full
  upstreams:
    - id: up1
      chain: ethereum
      group-labels: [full, fast]
      connectors:
        - type: json-rpc
          url: https://full-node.example.com
    - id: up2
      chain: ethereum
      group-labels: [archive]
      connectors:
        - type: json-rpc
          url: https://archive-node.example.com
```

By default nodecore balances every request across **all** of a chain's upstreams using the
[score-policy](#score-policy-config) rating. `label-balancing` layers an optional
**priority-group** mode on top: you tag upstreams with `group-labels` and declare an
`order` of those labels; requests are served from the highest-priority group first and fall
through to lower-priority groups when the current group cannot serve. Rating still decides
ordering **within** a group.

It is configured as a **global default** under `upstream-config.label-balancing` (applies to
every chain) and can be **overridden per chain** under `chain-defaults.<chain>.label-balancing`
(the per-chain block fully replaces the global one for that chain). When neither is set,
behavior is unchanged (pure rating).

> **⚠️ Requires a retry policy.** Falling through to the next group **after an error** is
> driven by retries: each retry re-selects an upstream, which is how the request advances
> within and across groups. Retries only happen when a [`failsafe-config`](#failsafe-config)
> `retry` policy is configured. Without one,
> a request makes a **single** upstream selection and will not advance on an error response —
> so `pass-on-error` and within-group error retries have no effect. (Falling through a group
> that is *entirely* unavailable at selection time still works without retries, since that
> happens within the single selection.) Set a `retry` policy with enough `attempts` to cover
> the upstreams you expect to traverse.

How groups are traversed:

- Groups are visited in `order`, then the default group (unlabeled upstreams) last.
- Within a group, upstreams keep the usual rating order.
- An upstream is selected **at most once per request**, even if it carries several labels and
  thus belongs to several groups.
- An entirely-dead group (every upstream unavailable / method banned / rate-limited) **always**
  falls through to the next group, regardless of `pass-on-error`.

`label-balancing` fields:

- `order` - Ordered list of group label names, highest priority first. **_Required_** when
  `label-balancing` is set; entries must be non-empty and unique.
- `pass-on-error` - Controls retry routing after a *retryable error response* (each error
  triggers a fresh upstream selection):
  - `false` (**_default_**) - retry **within the current group** (next-best untried upstream);
    advance to the next group only once the current group has no selectable upstream left.
  - `true` - **jump straight to the next group** on a retryable error, skipping any untried
    upstreams in the current group.
- `include-default` - When `true` (**_default_**), upstreams carrying none of the `order`
  labels form a final fallback group tried after all configured groups. When `false`, those
  upstreams are excluded from routing while label-balancing is active.

The per-upstream `group-labels` field (see [Fields](#fields)) assigns an upstream to one or
more groups. Labels not present in `order` route the upstream to the default group.

## upstreams

```yaml
upstreams:
  - id: my-super-upstream
    chain: ethereum
    connectors:
      - type: json-rpc
        url: https://path-to-eth-provider.com
        headers:
          my-header: my-header-value
  - id: full-upstream
    chain: polygon
    connectors:
      - type: json-rpc
        url: https://path-to-polygon-provider.com
        headers:
          my-header: my-header-value
      - type: websocket
        url: wss://path-to-polygon-provider.com
    head-connector: websocket
    poll-interval: 5s
    methods:
      ban-duration: 10m
      enable:
        - "my_method"
      disable:
        - "eth_getBlockByNumber"
    failsafe-config:
      retry:
        attempts: 10
        delay: 2s
        max-delay: 5s
        jitter: 3s
```

The `upstreams` section defines the actual blockchain providers that nodecore will route requests to.
Each upstream belongs to a specific chain, declares one or more connectors (HTTP/JSON-RPC, WebSocket, etc.) and have other specific settings.

### connectors

Each upstream can expose multiple interfaces for communication. A blockchain network may support various transports - JSON-RPC, WebSocket, REST, or gRPC - and the set of transports available for any given chain is declared by that chain's method spec (see [Method specs](11-method-specs.md)).

Supported connector types:

- `json-rpc` - HTTP-based JSON-RPC. Available on every chain family
- `websocket` - WebSocket-based JSON-RPC. Required for subscriptions and certain streaming requests (e.g. `eth_subscribe`)
- `rest` - REST endpoints. Used by chains whose canonical API is REST-shaped (e.g. Algorand, TRON, Aptos, and the Ethereum/Gnosis Beacon Chain). TRON additionally exposes an Ethereum-compatible `json-rpc` surface; you can configure either or both connectors on a TRON upstream — `rest` reaches `/wallet/*` (full node) and `/walletsolidity/*` (confirmed mirror), `json-rpc` reaches `/jsonrpc`. Aptos upstreams use `rest` exclusively, serving the fullnode `/v1/*` API
- `grpc` - gRPC endpoints (declared by spec on a per-chain basis)
- `rest-indexer` - a **self-contained indexer REST API** running next to the node API (e.g. the TON v3 indexer). This is a plain type: it may be an upstream's only connector (a standalone indexer upstream with its own head/health/bounds) or sit alongside the node-API connector on one upstream; see [TON deployment modes](#ton-deployment-modes)
- `rest-additional` - REST endpoints that augment a chain whose primary transport is something else (e.g. Hyperliquid). This is an *additional* connector: it cannot work standalone at all - an upstream cannot consist of only `rest-additional` connectors, at least one plain connector (`json-rpc` / `rest` / `grpc` / `websocket` / `rest-indexer`) must also be configured

By defining multiple connectors under one upstream, you give nodecore the flexibility to select the right transport for each incoming request.

Every upstream must also track its head (latest block / finalization state). The connector used for head tracking is selected as follows:

- If `head-connector` is set explicitly, that type is used
- Otherwise nodecore picks the best connector available on the upstream, where "best" depends on [`mode`](#mode):
  - `mode: default` - prefers the simplest type, in order `json-rpc` → `rest` → `grpc` → `websocket`
  - `mode: strict` - prefers the most capable type, in reverse order `websocket` → `grpc` → `rest` → `json-rpc`

`rest-additional` connectors are never chosen as the head connector.

### TON deployment modes

TON exposes two self-contained APIs: the **v2 HTTP API** (toncenter `ton-http-api`, connector type `rest`, base URL must include the `/api/v2` prefix) and the **v3 indexer** (toncenter `ton-indexer`, connector type `rest-indexer`, no path prefix). They have independent data windows — the v2 window is the backing liteserver's block retention, the v3 window is whatever range the indexer has been backfilled with — and independent failure modes (a stalled indexer does not affect the liteserver, and vice versa). Any combination is possible in practice: a non-archival node with a genesis-deep index, or the reverse.

Two deployment modes are supported:

- **Split (recommended)** - two upstreams of the same chain: a v2 upstream (single `rest` connector) and a v3 upstream (single `rest-indexer` connector — a plain type, fully legal on its own). Each upstream gets full independent accounting: its own head, health (v3: `masterchainInfo` freshness), chain validation (v2: zerostate hashes, v3: `global_id`), client labels, and lower bounds (v2: archival probe; v3: `first.seqno`, which may legally *decrease* when the index is backfilled deeper). Method routing between the two happens automatically: each upstream only advertises the methods its connector type carries.
- **Combined** - one upstream with both connectors. This works, but **all validations and calculations (head, health, chain validation, labels, lower bounds) are computed from the primary connector's API only** (the head/internal connector — `rest`, i.e. v2, in `mode: default`); the other connector serves its methods and takes no part in any accounting. nodecore logs a warning at upstream creation to make this explicit. Use this mode only when the v3 index's data window and health can be assumed to track the v2 node's (e.g. an indexer colocated with, and fed from, that same node).

```yaml
# Split mode (recommended)
upstreams:
  - id: ton-node
    chain: ton
    connectors:
      - type: rest
        url: http://ton-node:8081/api/v2
  - id: ton-index-v3
    chain: ton
    connectors:
      - type: rest-indexer
        url: http://ton-index:8082

# Combined mode (non-primary connector = methods only; logs a warning)
upstreams:
  - id: ton-combined
    chain: ton
    connectors:
      - type: rest
        url: http://ton-node:8081/api/v2
      - type: rest-indexer
        url: http://ton-index:8082
```

### Tor .onion upstreams

NodeCore supports connecting to upstreams hosted as Tor hidden services (`.onion` addresses) for both `json-rpc` and `websocket` connectors. This provides enhanced privacy and censorship resistance.

**Configuration requirements:**

1. Set `server.tor-url` in your config to point to a SOCKS5 proxy (usually a local Tor instance):

```yaml
server:
  tor-url: localhost:9050
```

2. Use `.onion` addresses in connector URLs:

```yaml
upstreams:
  - id: tor-upstream
    chain: ethereum
    connectors:
      - type: json-rpc
        url: http://examplehidden.onion
      - type: websocket
        url: ws://examplehidden.onion
```

When NodeCore detects a `.onion` hostname, it automatically routes the connection through the configured Tor proxy using SOCKS5. If `tor-url` is not set and a `.onion` upstream is configured, NodeCore will fail to start with an error.

## Fields

`upstreams` fields:

- `id` - Unique identifier of the upstream. **_Required_**, **_Unique_**
- `chain` - The chain this upstream serves (e.g. `ethereum`, `polygon`, `solana`, `algorand`, `aztec-mainnet`, `aptos-mainnet`). Must match values from [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml). **_Required_**
- `connectors` - The access endpoints for this upstream. **_Required_**, **_at least one_**. There can be only one connector of each type per upstream, and at least one connector must be a plain type (not `rest-additional`). Each connector has:
  - `type` - one of `json-rpc`, `websocket`, `rest`, `grpc`, `rest-additional`. **_Required_**
  - `url` - full endpoint URL. **_Required_**
  - `headers` - optional key/value map of extra headers to send with requests
  - `ca` - Path to a Certificate Authority (CA) certificate file to validate client certificates (for example, if you use self-signed certificates)
  - `response-header-deny` - list of upstream response-header names that must *not* be forwarded back to the client, on top of the built-in deny list (RFC 7230 hop-by-hop headers plus `Set-Cookie` and `Server`). Matching is case-insensitive
- `head-connector` - Connector type used to fetch chain head / finality information. Must match one of the connector types configured under `connectors`, and cannot be `rest-additional`
  - Example: `head-connector: websocket`
  - If not set, nodecore picks one according to the current [`mode`](#mode)
- `poll-interval` - Overrides the chain-default `poll-interval` for this specific upstream
- `options` - Overrides the chain-default `options` for this specific upstream. See [chain-defaults](#chain-defaults) for the full set of fields
- `methods` - Per-upstream method overrides. A method cannot be listed in both `enable` and `disable`:
  - `enable` - list of methods to explicitly allow
  - `disable` - list of methods to disable for this upstream
  - `ban-duration` - How long a method is excluded from this upstream after the upstream returns an error indicating the method does not exist or is unavailable. **_Default_**: `5m`

  > **⚠️ Connector scope (current limitation)**: Every entry in `enable` is applied to **all** of the API connectors declared by the chain's [method spec](11-method-specs.md). There is no per-connector targeting today, so on a chain that exposes the same method on multiple transports (e.g. EVM chains where the spec has both `json-rpc` and `websocket`), you cannot enable a method only on `json-rpc` while leaving it off on `websocket` - the flag toggles every connector at once.
  >
  > nodecore is chain-agnostic, and for chains that legitimately use several transports this is a real limitation: methods that are valid on one transport but not the other still need an explicit `api-connector` selector here. A future revision of this field will accept an `api-connector` qualifier so an entry like `eth_call@json-rpc` (or an equivalent structured form) can be scoped to a single transport. Until then, only configure `enable` for methods that share the same shape across every connector the chain advertises.
- `rate-limit-budget` - Reference to a shared rate limit budget defined in the top-level `rate-limit` section. See [Rate Limiting](06-rate-limiting.md) for details
- `rate-limit` - Inline rate limiting configuration specific to this upstream. Cannot be used together with `rate-limit-budget`. See [Rate Limiting](06-rate-limiting.md) for details
- `rate-limit-auto-tune` - Automatically adjusts the upstream's outgoing rate limit based on observed error rate and utilization. See [Rate Limiting](06-rate-limiting.md#auto-tune-rate-limiting) for the field semantics
- `failsafe-config` - Upstream-level failsafe configuration. Only the `retry` policy can be specified at this level (hedging and timeouts are configured globally on `upstream-config.failsafe-config`)
- `group-labels` - List of priority-group labels this upstream belongs to, used by [label-balancing](#label-balancing). These are **config-defined** labels, independent of the runtime labels produced by label detectors. An upstream may belong to several groups but is still selected at most once per request

## Validators and labels

Validators and label detectors run periodically (every `validation-interval`) against each upstream and feed into the availability / routing decision. They are toggled through the `chain-defaults.<chain>.options.*` flags listed above. The following table lists the validators that nodecore ships today and which flag turns each one off.

| Validator / detector | Chains | Flag to disable | What it does |
|---|---|---|---|
| Chain id / `net_version` | EVM | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Confirms the upstream is actually serving the configured chain. Fails at startup remove the upstream from the pool; runtime drift triggers re-removal |
| Aztec chain validator | Aztec | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Equivalent of chain-id check, using the Aztec node's chain-id endpoint |
| Aptos chain validator | Aptos | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Equivalent of chain-id check, using the `chain_id` from the Aptos ledger-info endpoint (`GET /v1`) |
| NEAR chain validator | NEAR | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Equivalent of chain-id check, comparing the `status` probe's `chain_id` against the configured chain |
| Ripple chain validator | Ripple | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Equivalent of chain-id check, using `network_id` from `server_state` (the field is omitted on mainnet — absent means `0`) |
| Starknet chain validator | Starknet | `disable-chain-validation`, `disable-settings-validation`, `disable-validation` | Equivalent of chain-id check, comparing the `starknet_chainId` hex-felt against the configured chain |
| `eth_syncing` validator | EVM | `validate-syncing` (set to `false`) or `disable-settings-validation` | Marks the upstream as syncing/unavailable when the node reports it is not fully synced |
| `net_peerCount` validator | EVM | `validate-peers` / `min-peers` or `disable-settings-validation` | Marks the upstream as unhealthy when peer count drops below `min-peers` |
| `eth_call` return-data limit | EVM | `validate-call-limit` or `disable-settings-validation` | Probes the upstream's maximum `eth_call` return-data size and marks it unhealthy if it is below `call-limit-size` |
| Health validator (EVM) | EVM | `disable-health-validation` | Generic liveness check appropriate to the chain family |
| Health validator (Solana) | Solana | `disable-health-validation` | Calls the Solana `getHealth` RPC and propagates the result |
| Health validator (Aztec) | Aztec | `disable-health-validation` | Probes Aztec node health |
| Health validator (Algorand) | Algorand | `disable-health-validation` | Probes Algorand node health |
| Health validator (Aptos) | Aptos | `disable-health-validation` | Calls the Aptos `GET /v1/-/healthy` endpoint and propagates the result |
| Health validator (Beacon) | Beacon Chain | `disable-health-validation` | Calls `GET /eth/v1/node/health` and marks the upstream available only on a 2xx response |
| Syncing validator (Beacon) | Beacon Chain | `validate-syncing` (set to `false`) | Probes `GET /eth/v1/node/syncing`; marks the upstream `Syncing` when `is_syncing` is true and `Unavailable` when `el_offline` is true |
| Peers validator (Beacon) | Beacon Chain | `validate-peers` / `min-peers` | Probes `GET /eth/v1/node/peer_count` and marks the upstream immature while connected peers are below `min-peers` |
| Health validator (NEAR) | NEAR | `disable-health-validation` | Calls the NEAR `status` RPC; marks the upstream `Syncing` when `sync_info.syncing` is true or `latest_block_time` is stale (stale-head guard). With `validate-peers` on, also probes `network_info` and marks the upstream `Unavailable` at zero `num_active_peers` |
| Health validator (Ripple) | Ripple | `disable-health-validation` | Maps the `server_state` ladder: `full`/`validating`/`proposing`/`tracking` = `Available`, `connected`/`syncing` = `Syncing`, anything else (e.g. `disconnected`) = `Unavailable`; `amendment_blocked` = `Unavailable` regardless of state. With `validate-peers` on, zero `peers` (from the same payload) = `Unavailable` |
| Health validator (Starknet) | Starknet | `disable-health-validation` | Calls `starknet_syncing`: a plain `false` is healthy, a sync object marks the upstream `Syncing` when the current-to-highest block lag exceeds the threshold. `validate-peers` has no effect (no p2p peer count — nodes sync from the feeder gateway) |
| Lower-bound detector | Solana, Algorand, Aztec, Aptos | `disable-lower-bounds-detection` | Determines the earliest available block / slot on the upstream so that queries against pruned ranges can be routed away |
| Lower-bound detector (Beacon) | Beacon Chain | `disable-lower-bounds-detection` | Binary-searches the earliest retained block, state, epoch (attestation rewards), and blob-sidecar slots so requests against pruned ranges are routed away |
| Lower-bound detector (NEAR) | NEAR | `disable-lower-bounds-detection` | Reads `sync_info.earliest_block_height` from `status` and publishes it as the state and block lower bounds (a sliding GC window on non-archival nodes) |
| Lower-bound detector (Ripple) | Ripple | `disable-lower-bounds-detection` | Publishes the low edge of the newest `complete_ledgers` range as the block lower bound; the bound may move *down* when an archive backfill deepens history |
| Lower-bound detector (Starknet) | Starknet | `disable-lower-bounds-detection` | Verified probe of block 1: success publishes `1` as the lower bound, failure emits an explicit `UnknownBound` |
| Label detectors (EVM) | EVM | `disable-labels-detection` | Populates upstream labels - client name & version, archive vs. full, gas limit, flashblock support, high-latency-tx capability. Labels are exposed via the [gRPC API](12-grpc-server.md) so external consumers can target upstreams with specific capabilities |
| Label detectors (Aptos) | Aptos | `disable-labels-detection` | Populates client name & version labels from the ledger-info endpoint (`GET /v1`) |
| Client label detector (Beacon) | Beacon Chain | `disable-labels-detection` | Reads `GET /eth/v1/node/version` and publishes the consensus-client type and version labels (Lighthouse, Prysm, Teku, Nimbus, etc.) |
| Client label detector (NEAR) | NEAR | `disable-labels-detection` | Reads `version.version` from `status` and publishes the client (`neard`) and version labels |
| Client label detector (Ripple) | Ripple | `disable-labels-detection` | Publishes `clio` with `clio_version` when present, otherwise `rippled` with `build_version` |
| Client label detector (Starknet) | Starknet | `disable-labels-detection` | Two-step probe: `pathfinder_version` first, then `juno_version`; the one that answers sets the client and version labels |

`disable-validation` is the master switch and overrides every per-validator flag.
