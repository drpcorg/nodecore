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
  - default-engine: memory
    budgets:
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

- `default-engine` - The rate limiting engine to use. Currently only `memory` is supported. **Required**
- `budgets` - Array of budget definitions. Each budget contains:
  - `name` - Unique identifier for the budget. **Required**, **Unique**
  - `engine` - Override the default engine for this specific budget (optional)
  - `config` - Rate limiting rules configuration. **Required**
    - `rules` - Array of rate limit rules (see Rules section below)

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

### Rule Fields

- `method` - Exact method name to match (e.g., `eth_getBlockByNumber`). **Either `method` or `pattern` must be specified**
- `pattern` - Regular expression pattern to match method names (e.g., `trace_.*` or `eth_getBlock.*`). **Either `method` or `pattern` must be specified**
- `requests` - Maximum number of requests allowed. **Required**, must be greater than 0
- `period` - Time window for the rate limit (e.g., `1s`, `1m`, `5m`). **Required**, must be greater than 0

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

```yaml
# Shared budgets
rate-limit-budgets:
  - default-engine: memory
    budgets:
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

## Validation

- Budget names must be unique and non-empty
- Referenced budgets must exist
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
