# Cache config guide

The `cache` section defines how nodecore stores and serves cached responses in order to reduce redundant requests to upstream providers and improve response times.

```yaml
cache:
  receive-timeout: 500ms
  connectors:
    - id: memory-connector
      driver: memory
      memory:
        max-items: 5000
        expired-remove-interval: 10s
  policies:
    - chain: "*"
      id: memory-policy-1
      method: "eth_getBlockByNumber"
      connector-id: memory-connector
      finalization-type: finalized
      ttl: 30s
```

The cache configuration is split into two main parts: `connectors` and `policies`:

- **Connectors** - Define the actual cache storage backends. Each connector specifies a driver (e.g., in-memory storage) and its settings (such as limits, cleanup intervals, etc.). Multiple connectors can be configured, and policies can decide which connector to use for a given request.
- **Policies** - Define the caching rules. A policy describes what to cache, how long to cache it, and where to store it (by linking to a connector). Policies allow fine-grained control, such as caching only finalized data, using method name patterns, or enforcing object size limits.

Together, connectors provide the storage layer and policies define the caching logic.

## Cache operations

nodecore performs two core cache operations: **Receive** and **Store**.

1. **Receive** – executed on every incoming request before it is forwarded upstream. A request key is computed as a hash of the method name and request parameters (if no parameters are present, only the method name is used). The **Receive** operation is executed in parallel across all cache policies, and if any policy returns a cached result, it is immediately returned to the client while the other lookups are canceled. A request will only hit the cache if it passes the following rules:
   - Requests with streamed responses are not cached. For example, `eth_getLogs` and Solana’s `getProgramAccounts` are streamed by default and are excluded. **In the future, all responses will be streamed by default, and streaming + caching will work together**
   - If the method spec explicitly sets `"cacheable": false`, the request is not cached. Example: `eth_sendRawTransaction` is never cached
   - Requests with block tags in the body (`latest`, `earliest`, etc.) are not cached because their responses are non-deterministic
   - If a policy has `finalization-type: finalized`, nodecore checks whether the requested block number is less than or equal to the chain’s finalized block. If the request targets a block beyond the finalized height, it will not be cached
   - If no policy matches the requested chain, the request is not cached
   - If no policy matches the requested method, the request is not cached
2. **Store** – executed on every response after it is received from an upstream. The same request key (computed from the method name and request parameters as described above) is used to associate the response with future cache lookups. The store operation is applied to each matching policy, and the response will only be cached if it passes the following rules:
   - The same basic rules described in the **Receive** operation apply (streamed responses, non-cacheable methods, block tags, finalization checks, etc.)
   - If a policy defines `object-max-size`, the response is measured, and if its size exceeds the configured value, it will not be cached
   - If a policy defines `cache-empty: true`, then responses that match one of the recognized empty values (`0x`, `[]`, `null`, `{}`) will also be cached

## Fields

- `receive-timeout` - Defines the maximum time nodecore waits for cache lookups during a **Receive** operation. If no cached result is returned within the configured timeout, the request continues to the upstream provider as usual. **_Default_**: `1s`

### connectors

```yaml
cache:
  connectors:
    - id: memory-connector
      driver: memory
      memory:
        max-items: 5000
        expired-remove-interval: 10s
    - id: redis-connector
      driver: redis
      redis:
        storage-name: redis-storage
    - id: postgresql-connector
      driver: "postgres"
      postgres:
        storage-name: postgres-storage
        query-timeout: 5s
```

The `connectors` section defines the cache storage backends. Each connector has an id (referenced by cache policies), a driver type that specifies how and where cached responses are stored and its settings.

`connector` fields:

- `id` - Unique identifier for the connector. **_Required_**, **_Unique_**
- `driver` - Defines the storage backend type. Currently supported: `memory`, `redis`, `postgres`

> **Note**: Redis and Postgres connectors now reference storages defined in the `app-storages` section. This allows sharing the same storage configuration across multiple components (cache, rate limiting, etc.).

The `memory` type is the simplest cache storage. All the items are stored inside the running nodecore process. The in-memory connector internally uses an LRU (Least Recently Used) cache algorithm. When the max-items limit is reached, the least recently used entries are evicted first.

#### Fields

- `max-items` - Maximum number of items to store in the in-memory cache. **_Default_**: `10000`
- `expired-remove-interval` - Interval at which expired cache entries are cleaned up. **_Default_**: `30s`

The `redis` connector provides a cache implementation backed by a Redis server. Currently, only a single Redis instance is supported.

Redis connectors reference a Redis storage defined in the `app-storages` section. This allows sharing the same Redis connection across multiple components (cache, rate limiting, etc.).

#### Fields

- `storage-name` - Reference to a Redis storage defined in `app-storages`. **_Required_**

For Redis storage configuration details, see [App Storages](07-app-storages.md).

The `postgres` connector provides a persistent cache implementation backed by a PostgreSQL database.

Postgres connectors reference a Postgres storage defined in the `app-storages` section. This allows sharing the same database connection across multiple components.

#### Fields

`storage-name` - Reference to a Postgres storage defined in `app-storages`. **_Required_**

- `query-timeout` - Maximum duration for executing SQL queries. **_Default_**: `300ms`
- `cache-table` - Name of the table used to store cache entries. Created automatically if it does not exist. **_Default_**: `cache_rpc`
- `expired-remove-interval` - Interval at which expired cache entries are cleaned up. **_Default_**: `30s`

For Postgres storage configuration details, see [App Storages](07-app-storages.md).

### policies

```yaml
policies:
  - chain: "*"
    id: memory-policy-1
    method: "eth_getBlockByNumber"
    connector-id: memory-connector
    finalization-type: finalized
    ttl: 30s
  - chain: "optimism|polygon|ethereum"
    id: memory-policy-2
    method: "debug*"
    finalization-type: none
    cache-empty: true
    connector-id: memory-connector
    object-max-size: "1000KB"
    ttl: 10s
```

The `policies` section defines the rules for which requests should be cached, how long they remain valid, and which connector should store them. Each policy is tied to specific chains and methods, allowing fine-grained caching control.

`policies` fields:

- `id` - Unique identifier for the policy. **_Required_**, **_Unique_**
- `chain` - Target blockchain(s) this policy applies to. **_Required_**. Possible values:
  - `*` matches all supported chains
  - Multiple chains can be specified with `|` (e.g., `optimism|polygon|ethereum`)
- `method` - RPC method or method pattern to which the policy applies. **_Required_**. Possible values:
  - exact names (`eth_getBlockByNumber`)
  - wildcards (`debug*` to cover all debug methods or `*` matches all methods)
- `connector-id` - References the id of a cache connector where results will be stored. **_Required_**
- `finalization-type` - Defines whether caching depends on blockchain finality:
  - `finalized` - only cache responses that are at or below the finalized block
  - `none` - no finalization check. **_Default_**
- `ttl` - Time-to-live for cached responses. Defines how long the entry stays in cache before being removed. **_Default_**: `10m` (10 minutes). If set to `0`, the cached item will never expire (cached indefinitely)
- `cache-empty` - If `true`, responses that are considered "empty" (`0x`, `[]`, `null`, `{}`) will also be cached. **_Default_**: `false`
- `object-max-size`- Maximum allowed size of the cached object. Responses larger than this value will not be cached. Supported units: `KB` and `MB` **_Default_**: `500KB`

## Example with App Storages

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379
  - name: postgres-storage
    postgres:
      url: postgres://user:pass@localhost:5432/db

cache:
  connectors:
    - id: redis-cache
      driver: redis
      redis:
        storage-name: redis-storage
    - id: postgres-cache
      driver: postgres
      postgres:
        storage-name: postgres-storage
        query-timeout: 5s
  policies:
    - chain: "*"
      id: policy-1
      method: "eth_getBlockByNumber"
      connector-id: redis-cache
      ttl: 30s
```

For complete `app-storages` configuration details, see [App Storages](07-app-storages.md).
