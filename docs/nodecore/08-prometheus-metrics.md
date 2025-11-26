# Prometheus Metrics Documentation

This document describes all Prometheus metrics exposed by dSheltie (NodeCore).

## Table of Contents

- [HTTP Metrics](#http-metrics)
- [Request Metrics](#request-metrics)
- [Upstream Metrics](#upstream-metrics)
- [Rate Limiter Metrics](#rate-limiter-metrics)
- [Cache Metrics](#cache-metrics)
- [WebSocket Metrics](#websocket-metrics)
- [Subscription Utilities Metrics](#subscription-utilities-metrics)

---

## HTTP Metrics

### `nodecore_http_time_to_last_byte`

**Type:** Histogram

**Description:** The histogram of HTTP request duration until the last byte is sent to the client.

**Labels:** None

**Source:** `internal/server/http_server.go`

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

**Source:** `internal/server/ws_server.go`

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
- `method` - The RPC method name
- `upstream` - The upstream ID

**Buckets:** [0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50]

**Source:** `internal/dimensions/tracker.go`

**Use Case:** Measure latency distribution for requests to specific upstreams.

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

**Description:** Current availability status of the upstream. Values: 0 = available, 1 = unavailable.

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
