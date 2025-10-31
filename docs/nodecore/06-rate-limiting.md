# Rate Limiting

Rate limiting provides per-method or pattern-based throttling of requests to upstream providers. When exceeded, returns HTTP `429` error.

Two configuration approaches:

1. **Rate Limit Budgets** - Shared configurations referenced by multiple upstreams
2. **Inline Rate Limiting** - Rules defined directly in upstream configuration

## Rate Limit Budgets

Reusable configurations for multiple upstreams.

### Configuration

```yaml
rate-limit-budgets:
  budgets:
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

- `budgets` - Array of budget group definitions. Each group contains:
  - `default-engine` - The rate limiting engine to use for budgets in this group (optional, defaults to `memory`)
  - `budgets` - Array of budget definitions. Each budget contains:
    - `name` - Unique identifier for the budget. **Required**, **Unique**
    - `engine` - Override the default engine for this specific budget (optional)
    - `config` - Rate limiting rules configuration. **Required**
      - `rules` - Array of rate limit rules (see Rules section below)

## Rate Limit Engines

Rate limit engines define the backend storage for rate limiting state. Two types are supported:

### Memory Engine

In-memory rate limiting (default). State is stored in the nodecore process and is not shared across instances.

```yaml
rate-limit-budgets:
  engines:
    - name: memory
      type: memory
```

### Redis Engine

Redis-based rate limiting. State is stored in Redis and can be shared across multiple nodecore instances. Requires a Redis storage to be configured in `app-storages`.

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379

rate-limit-budgets:
  engines:
    - name: redis
      type: redis
      redis:
        storage-name: redis-storage
```

For complete Redis storage configuration options, see [App Storages](07-app-storages.md).

#### Engine Configuration Fields

- `name` - Unique identifier for the engine. **Required**, **Unique**
- `type` - Engine type: `memory` or `redis`. **Required**
- `redis` - Redis engine configuration (required when `type: redis`):
  - `storage-name` - Reference to a Redis storage defined in `app-storages`. **Required**

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

### Memory Engine Example

```yaml
# Shared budgets with memory engine
rate-limit-budgets:
  budgets:
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

### Redis Engine Example

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379

rate-limit-budgets:
  engines:
    - name: redis
      type: redis
      redis:
        storage-name: redis-storage
  budgets:
    - default-engine: redis
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

For complete Redis storage configuration options (timeouts, pool settings, etc.), see [App Storages](07-app-storages.md).

## Validation

- Engine names must be unique and non-empty
- Engine type must be `memory` or `redis`
- Redis engines must reference an existing Redis storage in `app-storages`
- Redis engines must have `redis.storage-name` configured
- Memory engines cannot have `redis` configuration
- Budget names must be unique and non-empty across all budget groups
- Referenced budgets must exist
- Budget `engine` overrides must reference existing engines
- Budget `default-engine` must reference existing engines (if engines are defined)
- Rules must specify either `method` or `pattern`, not both
- `method` must be alphanumeric with underscores only (no regex)
- `pattern` must be valid regex
- `requests` > 0, `period` > 0
- Cannot use both `rate-limit-budget` and `rate-limit` on same upstream

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
