# App Storages

The `app-storages` section defines shared storage configurations that can be used by multiple components (cache connectors, rate limiting budgets, etc.). This allows you to define a Redis or Postgres connection once and reference it from multiple places.

## Configuration

```yaml
app-storages:
  - name: redis-storage
    redis:
      address: localhost:6379
      password: mypassword
      db: 0
  - name: postgres-storage
    postgres:
      url: postgres://user:pass@localhost:5432/dbname
```

## Storage Fields

- `name` - Unique identifier for the storage. **_Required_**, **_Unique_**
- `redis` - Redis storage configuration (mutually exclusive with `postgres`)
- `postgres` - Postgres storage configuration (mutually exclusive with `redis`)

## Redis Storage

Redis storage supports two modes: **single instance** and **cluster mode**.

### Single Instance Mode

For connecting to a single Redis server, use either `full-url` or `address`.

The URL follows the Go Redis library format: `redis://<user>:<password>@<host>:<port>/<db>?<query-params>`. Examples:

- redis://localhost:6379/0
- redis://:mypassword@127.0.0.1:6379/2
- redis://user:pass@redis.example.com:6379/1?read_timeout=2s&write_timeout=3s

Any parameters defined explicitly under `redis` (e.g. `timeouts`, `pool`) will override the corresponding values in full-url.

### Cluster Mode

For connecting to a Redis Cluster, use the `cluster` section with a list of cluster node addresses.

```yaml
app-storages:
  - name: redis-cluster
    redis:
      cluster:
        addresses:
          - node1.redis.example.com:6379
          - node2.redis.example.com:6379
          - node3.redis.example.com:6379
        route-by-latency: true
      password: mypassword
      timeouts:
        connect-timeout: 1s
      pool:
        size: 50
```

> **Note**: Cluster mode and single instance mode are mutually exclusive. You cannot use `cluster.addresses` together with `address` or `full-url`.

### Fields

**Single Instance Mode:**

- `full-url` - Full connection URL in Go Redis format — `redis://<user>:<password>@<host>:<port>/<db>?<query-params>`
- `address` - Host and port of the Redis instance. Either `full-url` or `address` must be specified for single instance mode
- `db` - Database index. **_Default_**: `0`

**Cluster Mode:**

- `cluster.addresses` - List of Redis cluster node addresses (host:port). At least one address is required for cluster mode
- `cluster.route-by-latency` - Route read commands to the node with the lowest latency. **_Default_**: `false`
- `cluster.route-randomly` - Route read commands to random nodes. **_Default_**: `false`
- `cluster.read-only` - Enable read-only mode for replica nodes. **_Default_**: `false`

**Common Fields (both modes):**

- `username` - Optional username for Redis authentication
- `password` - Password for Redis authentication
- `timeouts.connect-timeout` - Maximum duration for establishing a connection to the Redis server. **_Default_**: `500ms`
- `timeouts.read-timeout` - Timeout for reading a response from Redis. **_Default_**: `200ms`
- `timeouts.write-timeout` - Timeout for writing data to Redis. **_Default_**: `200ms`
- `pool.size` - Total number of connections that can be maintained in the pool. **_Default_**: `10 × runtime.GOMAXPROCS(0)`
- `pool.pool-timeout` - Maximum wait time for acquiring a connection from the pool when all connections are busy. **_Default_**: `timeouts.read-timeout` + `1s`
- `pool.min-idle-conns` - Minimum number of idle connections maintained by the pool. **_Default_**: `0`
- `pool.max-idle-conns` - Maximum number of idle connections maintained by the pool. **_Default_**: `0`
- `pool.max-active-conns` - Maximum number of connections allocated by the pool at a given time
- `pool.conn-max-idle-time` - Maximum time a connection can remain idle. **_Default_**: `30m`
- `pool.conn-max-life-time` - Maximum time a connection may be reused

## Postgres Storage

### Fields

- `url` - Full PostgreSQL connection string in standard DSN format: `postgres://<user>:<password>@<host>:<port>/<dbname>?<query-params>`. All parameters supported by the underlying Go PostgreSQL library can be included. **_Required_**

## Complete Example

```yaml
app-storages:
  - name: shared-redis
    redis:
      full-url: "redis://localhost:6379/0"
      timeouts:
        connect-timeout: 1s
        read-timeout: 2s
        write-timeout: 3s
      pool:
        size: 35
        min-idle-conns: 10
        max-idle-conns: 50
  - name: shared-postgres
    postgres:
      url: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable

cache:
  connectors:
    - id: redis-cache
      driver: redis
      redis:
        storage-name: shared-redis
    - id: postgres-cache
      driver: postgres
      postgres:
        storage-name: shared-postgres

rate-limit:
  - default-storage: shared-redis
    budgets:
      - name: redis-budget
        config:
          rules:
            - method: eth_call
              requests: 100
              period: 1s
```

## Usage

Storages defined in `app-storages` can be referenced by:

- **Cache connectors** - Redis and Postgres cache backends (see [Cache](04-cache.md))
- **Rate limit budgets** - Redis-based rate limiting (see [Rate Limiting](06-rate-limiting.md))
