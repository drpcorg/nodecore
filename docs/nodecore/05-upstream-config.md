# Upstream config guide

This `upstream-config` section defines how nodecore discovers, evaluates, and interacts with upstream blockchain providers.

```yaml
upstream-config:
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

It brings together:
1. Failsafe configuration (`failsafe-config`) - Global resilience settings: retries (attempts, backoff, max delay, jitter) and hedging (duplicate a slow request after a delay, with a cap on parallel hedges).
2. Chain defaults (`chain-defaults`) - Per-chain operational defaults, such as poll-interval used for chain-specific activities (e.g., head/finality polling).
3. Scoring policy (`score-policy-config`) - Controls how upstream health/quality is calculated: a calculation interval and a scoring function. The score blends metrics like latency and error rate and is used by the router to pick the best upstream.
4. Upstreams (`upstreams`) - The actual provider entries.
5. Integrity (`integrity`) ensures that methods like eth_blockNumber and eth_getBlockByNumber never return stale data. When enabled, NodeCore guarantees non-decreasing block numbers by validating responses against the current head and retrying with the highest-synced upstream if needed.

Together, these settings let you (1) register providers, (2) tune resiliency and polling, and (3) define how nodecore scores and selects the best upstream at runtime.

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
   * If `hedge.delay` elapses with no response, the execution flow may launch a hedge (speculative parallel request), up to `hedge.max` additional copies. Hedges can target other upstreams.
3. First success wins. As soon as any in-flight attempt (original or hedge) returns a successful response, the executor returns it and cancels other in-flight attempts.
4. Retry on retryable errors. If an attempt returns a retryable error, the execution flow applies the retry policy. Non-retryable errors end the flow immediately (returned to the client).

nodecore uses the [failsafe-go](https://failsafe-go.dev/) library for resiliency primitives. At the moment we rely on:
* Retry policy – provided by failsafe-go
* Hedge policy – [custom implementation](../../internal/resilience/parallel_hedge.go) (instead of the default) for stricter latency semantics

**Why a custom hedge policy?**

The baseline hedging behavior has a few drawbacks for our use case:
* If an immediate error arrives from the primary request, the default behavior may still wait for the hedge delay (or cascade hedges serially), slowing error paths.
* Hedge requests may be issued sequentially, effectively waiting for each attempt instead of firing truly in parallel after the delay.

To eliminate these issues, nodecore implements a pure hedging strategy with clear timing and parallelism guarantees:
* Delay gate - A hedged request is sent only if `hedge.delay` has fully elapsed and the primary has not completed successfully. If we receive any response (success or error) before `hedge.delay` elapses, we return it immediately (no hedges launched)
* Parallel launch - Once `hedge.delay` elapses with no success, we launch up to `hedge.max` parallel hedges immediately (not one-by-one)
* First-success-wins - As soon as any in-flight attempt (primary or any hedge) returns success, we return that response and cancel all other in-flight attempts.

`failsafe-config` fields:
1. The `retry` section:
   * `attempts` - Maximum number of request attempts, including the initial one. **_Default_**: `3`
   * `delay` - Base wait time before a retry. Used as the starting backoff. **_Defaults_**: `300ms`
   * `max-delay` - Upper bound for retry backoff. The effective delay will never exceed this value
   * `jitter` - Adds randomization to each backoff
2. The `hedge` section:
   * `delay` - How long to wait after sending the initial request before launching hedged requests. Can't be less than 50ms. **_Default_**: `1s`
   * max - Maximum number of additional parallel hedged requests to launch once the delay has elapsed. **_Default_**: `2`

## chain-defaults

```yaml
chain-defaults:
  ethereum:
    poll-interval: 45s
  polygon:
    poll-interval: 30s
```

The `chain-defaults` section defines per-chain baseline settings that apply to all upstreams of that chain unless explicitly overridden in the upstream configuration.

`chain-defaults` fields:
* `<chain>.poll-interval` - Defines how often nodecore polls the upstreams for that chain to fetch new head/finality information
  * Example: `ethereum.poll-interval: 45s` means all Ethereum upstreams are polled every 45 seconds unless overridden. The **_default_** `poll-interval` value globally is `1m` (1 minute)

> **⚠️ Note**: Chain names in this section must match the identifiers defined in [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml)

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
   * Latency percentiles: p90, p95, p99
   * Request statistics: total requests, total errors, error rate, successful retries
   * Blockchain state metrics: head lag (distance from the latest head), finalization lag (distance from the latest finalized block)
2. Rating subsystem. At each `calculation-interval`, the rating subsystem invokes a scoring function that calculates the upstreams' rating based on these metrics.
    * By default, a built-in function (e.g. defaultLatencyErrorRatePolicyFunc) is used. [All default functions](../../internal/config/default_ts_funcs.go)
    * Optionally, you can provide a custom TypeScript function that defines your own rating logic
3. Execution flow - the execution flow itself does not evaluate upstreams; it simply picks the best one according to the latest rating.

**Writing a custom scoring function with the following rules**:
1. Function signature
```typescript
function sortUpstreams(upstreamData: UpstreamData[]): SortResponse
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
* `calculation-interval` - How often the scoring subsystem recalculates upstream scores. **_Defaults_**: `10s`
* `calculation-function-name` - The name of a built-in scoring function to use. Possible functions - `defaultLatencyPolicyFunc`, `defaultLatencyErrorRatePolicyFunc`. **_Default_**: `DefaultLatencyPolicyFuncName`
* `calculation-function-file-path` - Path to a custom TypeScript file implementing your own scoring function

> **⚠️ Note**: Both `calculation-function-name` and `calculation-function-file-path` can't be set at the same time.

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

Each upstream can expose multiple interfaces for communication. A blockchain network may support various protocols such as JSON-RPC, WebSocket, REST, or gRPC.
nodecore is designed to support all of these, so that depending on the request type, the most suitable connector can be used automatically.

Currently supported:
* `json-rpc`– standard HTTP-based JSON-RPC API
* `websocket` – WebSocket-based JSON-RPC API, required for subscriptions and certain streaming requests

Planned support:
* `rest` – REST endpoints for chains that expose REST APIs (e.g., Cosmos chains, beacon chains, etc.)
* `grpc` – gRPC endpoints for chains/protocols where it is available

By defining multiple connectors under one upstream, you give nodecore the flexibility to select the right transport for each request.

In addition, every upstream must track its head (latest block / finalization state). For this, nodecore needs to know which connector should be used:
* You can explicitly specify this via `head-connector`
* If not specified and multiple connectors are defined, nodecore chooses based on the following priority:
  * `json-rpc`
  * `rest`
  * `grpc`
  * `websocket`

This ensures that the most stable/compatible connector is used for head tracking by default, but you can override the behavior when needed.

## Fields

`upstreams` fields:
* `id` - Unique identifier of the upstream. **_Required_**, **_Unique_**
* `chain` - The chain this upstream serves, (e.g. `ethereum`, `polygon`). Must match values from [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml). **_Required_**
* `connectors` - Defines the access endpoints for this upstream. **_Required_**. Each connector has:
  * `type` - supported values: `json-rpc`, `websocket`. **_Required_**
  * `url` - full endpoint URL. **_Required_**
  * `headers` - optional key/value map of extra headers to send with requests
* `head-connector` - Specifies which connector is used to fetch chain head/finality values
  * Example: `head-connector: websocket`
  * If not set, nodecore picks a default depending on connector types
* `poll-interval` - Overrides the `chain-defaults.poll-interval` for this specific upstream. **_Default_**: `1m` (1 minute)
* `methods` - Allows per-upstream method overrides. A method cannot be listed in both `enable` and `disable`:
  * `enable` – list of methods to explicitly allow
  * `disable` – list of methods to disable for this upstream
  * `ban-duration` - specifies how long a method should remain banned for a given upstream after encountering an error that indicates the method does not exist or is unavailable. During this period, NodeCore will not send requests for that method to the affected upstream. **_Default_**: `5m` (5 minutes)
* `failsafe-config` - failsafe-config that is applied on the upstream level. Only the `retry` policy can be specified