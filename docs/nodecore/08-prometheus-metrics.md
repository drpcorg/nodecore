# Prometheus Metrics Documentation

This document describes all Prometheus metrics exposed by nodecore on the `metrics-port` defined in [Server config](02-server-config.md). The endpoint is `GET /metrics`.

## Table of Contents

- [HTTP Metrics](#http-metrics)
- [Request Metrics](#request-metrics)
- [Upstream Metrics](#upstream-metrics)
- [Quorum Metrics](#quorum-metrics)
- [Rate Limiter Metrics](#rate-limiter-metrics)
- [Cache Metrics](#cache-metrics)
- [WebSocket Metrics](#websocket-metrics)
- [Subscription Utilities Metrics](#subscription-utilities-metrics)
- [Logs Subscription Metrics](#logs-subscription-metrics)

---

## HTTP Metrics

### `nodecore_http_time_to_last_byte`

**Type:** Histogram

**Description:** The histogram of HTTP request duration until the last byte is sent to the client.

**Labels:** None

**Source:** `internal/server/http_server/http_server.go`

**Use Case:** Monitor HTTP response latency and identify slow requests.

---

## Request Metrics

### `nodecore_request_requests_total`

**Type:** Counter

**Description:** Total number of RPC requests sent across all upstreams.

**Labels:**

- `chain` - The blockchain network (e.g., ethereum, solana)
- `method` - The RPC method name (e.g., eth_getBlockByNumber)

**Source:** `internal/upstreams/flow/execution_flow.go`

**Use Case:** Track the total volume of requests per chain and method across all upstreams.

---

### `nodecore_request_errors_total`

**Type:** Counter

**Description:** The total number of RPC request errors returned by all upstreams.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name

**Source:** `internal/upstreams/flow/execution_flow.go`

**Use Case:** Monitor error rates aggregated across all upstreams for specific methods and chains.

---

### `nodecore_request_hedge_hit`

**Type:** Counter

**Description:** The total number of hedged RPC requests executed on an upstream. Hedging occurs when a request is sent to multiple upstreams simultaneously to reduce latency.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `upstream` - The upstream ID that triggered the hedge

**Source:** `internal/upstreams/flow/request_processor.go`

**Use Case:** Track how often the hedging mechanism is triggered, indicating slow responses from primary upstreams.

---

### `nodecore_request_cache_hit`

**Type:** Counter

**Description:** The total number of RPC requests served from cache instead of forwarding to upstreams.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name

**Source:** `internal/caches/cache_processor.go`

**Use Case:** Measure cache effectiveness and reduce upstream load.

---

### `nodecore_request_ws_connections`

**Type:** Gauge

**Description:** The total number of active websocket connections from clients.

**Labels:**

- `chain` - The blockchain network

**Source:** `internal/server/http_server/ws_server.go`

**Use Case:** Monitor the number of concurrent websocket connections per chain.

---

### `nodecore_request_json_ws_connections`

**Type:** Gauge

**Description:** The current number of active JSON-RPC subscriptions (upstream websocket connections).

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID
- `subscription` - The subscription type (e.g., newHeads, logs)

**Source:** `internal/upstreams/ws/ws_connection.go`

**Use Case:** Track active subscriptions to upstream websocket providers.

---

### `nodecore_request_json_ws_operations`

**Type:** Gauge

**Description:** The current number of active websocket operations (pending requests and subscriptions) with an upstream.

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Source:** `internal/upstreams/ws/ws_connection.go`

**Use Case:** Monitor the workload and concurrent operations per upstream websocket connection.

---

## Upstream Metrics

### `nodecore_upstream_requests_total`

**Type:** Counter

**Description:** The total number of RPC requests sent to a specific upstream.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `upstream` - The upstream ID

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Track request volume per individual upstream to identify load distribution.

---

### `nodecore_upstream_errors_total`

**Type:** Counter

**Description:** The total number of RPC request errors returned by a specific upstream.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `upstream` - The upstream ID

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Monitor error rates per upstream to identify problematic providers.

---

### `nodecore_upstream_successful_retries_total`

**Type:** Counter

**Description:** The total number of RPC requests that succeeded after being retried.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `upstream` - The upstream ID

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Track retry effectiveness and identify upstreams that frequently require retries.

---

### `nodecore_upstream_request_duration`

**Type:** Histogram

**Description:** The duration of RPC requests to upstreams in seconds.

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Buckets:** [0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50]

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Measure latency distribution for requests to specific upstreams.

> **Note:** unlike the request counters, this histogram deliberately has no `method` label — each histogram label combination produces a series per bucket, so a per-method histogram would make the `/metrics` exposition grow with the number of methods × upstreams and eventually exceed scrapers' response-size limits. Per-method latency quantiles are still tracked internally and used for upstream rating.

---

### `nodecore_upstream_blocks`

**Type:** Gauge

**Description:** The current block height of a specific block type tracked by an upstream.

**Labels:**

- `upstream` - The upstream ID
- `blockType` - The block type (e.g., latest, finalized, safe)
- `chain` - The blockchain network

**Source:** `internal/upstreams/upstream.go`

**Use Case:** Monitor block synchronization status for different block types across upstreams.

---

### `nodecore_upstream_heads`

**Type:** Gauge

**Description:** The current head block height tracked by an upstream.

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Source:** `internal/upstreams/upstream.go`

**Use Case:** Monitor the current head block height that each upstream reports.

---

### `nodecore_upstream_head_lag`

**Type:** Gauge

**Description:** The block lag of an upstream compared to the current head (how many blocks behind).

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Identify upstreams that are falling behind the network head.

---

### `nodecore_upstream_finalization_lag`

**Type:** Gauge

**Description:** The block lag of an upstream compared to the current finalization block.

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Track how far behind an upstream is in terms of finalized blocks.

---

### `nodecore_upstream_availability_status`

**Type:** Gauge

**Description:** Current availability status of the upstream. Values: 1 = available, 2 = immature, 3 = syncing, 4 = unavailable.

**Labels:**

- `chain` - The blockchain network
- `upstream` - The upstream ID

**Source:** `internal/upstreams/chain_supervisor.go`

**Use Case:** Monitor upstream availability and detect when upstreams become unavailable.

---

### `nodecore_upstream_rating`

**Type:** Gauge

**Description:** The current rating score of an upstream for a specific chain and method, calculated by the rating policy.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `upstream` - The upstream ID

**Source:** `internal/rating/registry.go`

**Use Case:** Monitor upstream quality scores used for intelligent request routing.

---

## Quorum Metrics

### `nodecore_quorum_verifications_total`

**Type:** Counter

**Description:** Outcomes of QR signature verification for client-requested quorum reads. Incremented once per verified response signature. See [Quorum](10-quorum.md) for the request shape that produces these counters.

**Labels:**

- `chain` - The blockchain network
- `method` - The RPC method name
- `status` - `ok` or `fail`
- `reason` - Failure reason when `status=fail`. Possible values include `missing_signatures`, `insufficient_signatures`, `invalid_signature`, `unknown_provider`, `malformed_header`, `request_id_mismatch`, `not_supported`

**Source:** `internal/upstreams/flow/execution_flow.go`

**Use Case:** Monitor how often quorum verification succeeds vs. fails, and break down failures by reason to spot provider-key drift or signature format issues.

---

## Rate Limiter Metrics

### `nodecore_ratelimiter_rate_limit_budget_requests`

**Type:** Counter

**Description:** The number of requests checked against a rate limit budget.

**Labels:**

- `budget` - The rate limit budget name
- `method` - The RPC method name

**Source:** `internal/ratelimiter/budget.go`

**Use Case:** Track rate limit budget usage per method.

---

### `nodecore_ratelimiter_rate_limit_budget_exceeded`

**Type:** Counter

**Description:** The number of requests that exceeded the rate limit budget and were rejected.

**Labels:**

- `budget` - The rate limit budget name
- `method` - The RPC method name

**Source:** `internal/ratelimiter/budget.go`

**Use Case:** Monitor rate limiting effectiveness and identify methods hitting limits.

---

### `nodecore_ratelimiter_auto_tune_tuned_rate_limit`

**Type:** Gauge

**Description:** The current auto-tuned rate limit for an upstream. This value is dynamically adjusted based on upstream performance and error rates.

**Labels:**

- `upstream` - The upstream ID
- `period` - The rate limit period (e.g., "1s", "1m")

**Source:** `internal/ratelimiter/upstream_autotune.go`

**Use Case:** Monitor the dynamic rate limit adjustments for upstreams with auto-tuning enabled.

---

## Cache Metrics

See [Request Metrics](#request-metrics) section for `nodecore_request_cache_hit`.

---

## WebSocket Metrics

See [Request Metrics](#request-metrics) section for websocket-related metrics.

---

## Subscription Utilities Metrics

These metrics track internal subscription manager performance (used for event propagation within the system).

### `chanutil_subscriptions_rate_events`

**Type:** Counter

**Description:** The rate of events published through internal subscription channels.

**Labels:**

- `source` - The subscription manager source name

**Source:** `pkg/utils/subscriptions.go`

**Use Case:** Monitor internal event propagation rate.

---

### `chanutil_subscriptions_num`

**Type:** Gauge

**Description:** The number of active internal subscriptions.

**Labels:**

- `source` - The subscription manager source name

**Source:** `pkg/utils/subscriptions.go`

**Use Case:** Track the number of internal subscribers for debugging and performance analysis.

---

### `chanutil_unread_messages_num`

**Type:** Gauge

**Description:** The number of unread messages in internal subscription channels.

**Labels:**

- `source` - The subscription source name

**Source:** `pkg/utils/subscriptions.go`

**Use Case:** Identify potential backpressure or slow consumers in internal event systems.

---

## Logs Subscription Metrics

Metrics for the locally-synthesized EVM `logs` subscription source (one shared `eth_getLogs` per block, fanned out to all subscribers). A non-zero value on either counter means subscribers may have silently missed log events.

### `nodecore_logs_source_blocks_skipped_total`

**Type:** Counter

**Description:** The total number of blocks whose logs could not be served and were skipped. The block's logs are silently missing from every `logs` subscriber on that chain.

**Labels:**

- `chain` - The blockchain network (e.g., ethereum)
- `reason` - Why the block was skipped: `build` (failed to build the `eth_getLogs` request), `no_upstream` (no upstream at the block height / strategy exhausted), `parse` (failed to parse the `eth_getLogs` result), `upstream_error` (every attempt returned an upstream error)

**Source:** `internal/upstreams/flow/logs_source.go`

**Use Case:** Alert on gaps in delivered `logs`; a sustained `no_upstream` rate indicates insufficient upstream coverage at the chain head.

---

### `nodecore_logs_source_reorg_clamped_total`

**Type:** Counter

**Description:** The total number of reorgs whose orphaned suffix reached deeper than the history window, so the oldest orphaned blocks were never re-emitted with `removed:true`.

**Labels:**

- `chain` - The blockchain network (e.g., ethereum)

**Source:** `internal/upstreams/flow/subengine/blockupdates.go`

**Use Case:** Detect deep reorgs that exceed the reconciliation window, where some `removed` events are silently dropped.
