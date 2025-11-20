# Rate Limiting

Rate limiting provides per-method or pattern-based throttling of requests to upstream providers. When exceeded, returns HTTP `429` error.

Two configuration approaches:

1. **Rate Limit Budgets** - Shared configurations referenced by multiple upstreams
2. **Inline Rate Limiting** - Rules defined directly in upstream configuration

## Rate Limit Budgets

Reusable configurations for multiple upstreams.

### Configuration

```yaml
rate-limit:
  - budgets:
      - name: standard-budget
        config:
          rules:
            - method: eth_getBlockByNumber
              requests: 100
              period: 1s
            - pattern: trace_.*
              requests: 5
              period: 2m
      - name: premium-budget
        config:
          rules:
            - method: eth_call
              requests: 500
              period: 1s
            - pattern: eth_.*
              requests: 1000
              period: 1s
```

### Budget Configuration Fields

- `rate-limit` - Array of budget group definitions. Each group contains:
  - `default-storage` - The Redis storage name to use for budgets in this group (optional, defaults to in-memory rate limiting)
  - `budgets` - Array of budget definitions. Each budget contains:
    - `name` - Unique identifier for the budget. **Required**, **Unique**
    - `storage` - Override the default storage for this specific budget (optional). Must reference a Redis storage from `app-storages`
    - `config` - Rate limiting rules configuration. **Required**
      - `rules` - Array of rate limit rules (see Rules section below)

## Rate Limit Storage

Rate limiting state can be stored either in-memory (default) or in Redis for distributed rate limiting across multiple nodecore instances.

### Memory (Default)

In-memory rate limiting. State is stored in the nodecore process and is not shared across instances. This is the default when no storage is specified.

```yaml
rate-limit:
  - budgets:
      - name: standard-budget
        # No storage specified = in-memory
        config:
          rules:
            - method: eth_call
              requests: 100
              period: 1s
```

### Redis Storage

Redis-based rate limiting. State is stored in Redis and can be shared across multiple nodecore instances. Requires a Redis storage to be configured in `app-storages`.

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379

rate-limit:
  - default-storage: redis-storage
    budgets:
      - name: redis-budget
        config:
          rules:
            - method: eth_getBalance
              requests: 200
              period: 1s
```

For complete Redis storage configuration options, see [App Storages](07-app-storages.md).

### Usage

```yaml
upstream-config:
  upstreams:
    - id: eth-upstream-1
      chain: ethereum
      rate-limit-budget: standard-budget
      connectors:
        - type: json-rpc
          url: https://provider1.example.com

    - id: eth-upstream-2
      chain: ethereum
      rate-limit-budget: premium-budget
      connectors:
        - type: json-rpc
          url: https://provider2.example.com
```

## Inline Rate Limiting

Define rate limits directly in upstream configuration.

```yaml
upstream-config:
  upstreams:
    - id: eth-upstream
      chain: ethereum
      rate-limit:
        rules:
          - method: eth_getBlockByNumber
            requests: 100
            period: 1s
          - pattern: eth_getBlockByHash|eth_getBlockByNumber
            requests: 50
            period: 1s
          - pattern: trace_.*
            requests: 5
            period: 2m
      connectors:
        - type: json-rpc
          url: https://test.com
```

> **⚠️ Note**: An upstream can use either `rate-limit-budget` (reference to a shared budget) OR `rate-limit` (inline configuration), but not both.

## Rate Limit Rules

Rate limit rules define the throttling behavior for specific methods or method patterns.

### Rule Fields

- `method` - Exact method name to match (e.g., `eth_getBlockByNumber`). **Either `method` or `pattern` must be specified**
- `pattern` - Regular expression pattern to match method names (e.g., `trace_.*` or `eth_getBlock.*`). **Either `method` or `pattern` must be specified**
- `requests` - Maximum number of requests allowed. **Required**, must be greater than 0
- `period` - Time window for the rate limit (e.g., `1s`, `1m`, `5m`). **Required**, must be greater than 0

### Multiple Rules and Overlapping Patterns

**Important**: Multiple rules can match the same method, and **all matching rules will be evaluated**. A request will only proceed if it passes all applicable rate limits.

For example, if you configure:

```yaml
rules:
  - pattern: eth_.*
    requests: 1000
    period: 1s
  - method: eth_getBlockByNumber
    requests: 100
    period: 1s
```

A request to `eth_getBlockByNumber` will be checked against **both** rules:

1. The general `eth_.*` pattern limit (1000 req/s)
2. The specific `eth_getBlockByNumber` limit (100 req/s)

The request will be rate limited if **any** of the matching rules is exceeded.

### Examples

**Exact method matching:**

```yaml
rules:
  - method: eth_call
    requests: 100
    period: 1s
```

**Pattern-based (regular expression) matching:**

```yaml
rules:
  # Match all trace methods
  - pattern: trace_.*
    requests: 5
    period: 1m

  # Match specific methods with alternation
  - pattern: eth_getBlockByHash|eth_getBlockByNumber
    requests: 50
    period: 1s

  # Match all eth methods
  - pattern: eth_.*
    requests: 1000
    period: 1s
```

## Examples

### Memory (In-Process) Example

```yaml
# Shared budgets with in-memory storage
rate-limit:
  - budgets:
      - name: standard
        config:
          rules:
            - method: eth_getBlockByNumber
              requests: 100
              period: 1s
            - pattern: trace_.*
              requests: 5
              period: 2m

upstream-config:
  upstreams:
    # Using budget reference
    - id: eth-1
      chain: ethereum
      rate-limit-budget: standard
      connectors:
        - type: json-rpc
          url: https://provider1.com

    # Using inline config
    - id: eth-2
      chain: ethereum
      rate-limit:
        rules:
          - pattern: .*
            requests: 1000
            period: 1s
      connectors:
        - type: json-rpc
          url: https://provider2.com
```

### Redis Storage Example

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379

rate-limit:
  - default-storage: redis-storage
    budgets:
      - name: redis-budget
        config:
          rules:
            - method: eth_getBalance
              requests: 200
              period: 1s

upstream-config:
  upstreams:
    - id: eth-upstream
      chain: ethereum
      rate-limit-budget: redis-budget
      connectors:
        - type: json-rpc
          url: https://provider.com
```

### Mixed Storage Example

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379

rate-limit:
  - budgets:
      - name: memory-budget
        # No storage specified = in-memory
        config:
          rules:
            - method: eth_call
              requests: 100
              period: 1s
      - name: redis-budget
        storage: redis-storage
        config:
          rules:
            - method: eth_getBalance
              requests: 200
              period: 1s

upstream-config:
  upstreams:
    - id: eth-1
      chain: ethereum
      rate-limit-budget: memory-budget
      connectors:
        - type: json-rpc
          url: https://provider1.com
    - id: eth-2
      chain: ethereum
      rate-limit-budget: redis-budget
      connectors:
        - type: json-rpc
          url: https://provider2.com
```

For complete Redis storage configuration options (timeouts, pool settings, etc.), see [App Storages](07-app-storages.md).

## Auto-Tune Rate Limiting

Auto-tune dynamically adjusts the rate of **outgoing requests from nodecore to a specific upstream** based on the upstream's actual capacity and rate limiting behavior.

### Purpose

Most RPC providers implement their own rate limiting on their side. When nodecore exceeds the provider's rate limit, those requests fail with errors (typically HTTP 429 or similar). Auto-tune solves this by:

1. **Avoiding Wasted Requests** - Prevents sending requests that will be rejected by the upstream's rate limiter
2. **Finding Optimal Rate** - Automatically discovers the maximum request rate the upstream can handle

> **Important**: This is a client-side rate limit that controls how fast nodecore sends requests to the upstream provider. It does NOT limit requests from your users to nodecore.

### How It Works

Auto-tune monitors the upstream's responses and adjusts the outgoing request rate using the following logic:

1. **Decrease Limit** - When error rate from upstream exceeds the configured threshold, reduce the request rate to avoid hitting the upstream's rate limit
2. **Increase Limit** - When there are no errors from upstream and either:
   - Peak utilization exceeds 95% (we can safely send more requests)
   - Too many requests are being blocked locally (our limit is too conservative)
3. **Stable** - No change when error rate and utilization are within acceptable ranges

### Configuration

Auto-tune is configured per upstream:

```yaml
upstream-config:
  upstreams:
    - id: eth-upstream
      chain: ethereum
      rate-limit-auto-tune:
        enabled: true
        period: 1m
        error-threshold: 0.1
        init-rate-limit: 100
        init-rate-limit-period: 1s
      connectors:
        - type: json-rpc
          url: https://provider.example.com
```

### Configuration Fields

- `enabled` - Enable auto-tune for this upstream. **Required**, defaults to `false`
- `period` - How often to recalculate the rate limit (e.g., `30s`, `1m`, `5m`). **Optional**, defaults to `1m`
- `error-threshold` - Error rate threshold (0.0 to 1.0) that triggers limit reduction. **Optional**, defaults to `0.1` (10%)
- `init-rate-limit` - Initial rate limit to start with. **Optional**, defaults to `100`
- `init-rate-limit-period` - Time window for the rate limit (e.g., `1s`, `100ms`). **Optional**, defaults to `1s`

## Error Response

HTTP `429` with JSON-RPC error:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": 429,
    "message": "rate limit exceeded"
  }
}
```
