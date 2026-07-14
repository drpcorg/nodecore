# nodecore Configuration guide

The configuration file is the entry point for all nodecore settings. It is organized into several sections, each responsible for a specific part of the system:

- [Server](02-server-config.md) - basic runtime settings of the nodecore server (HTTP, gRPC, metrics, profiling, TLS)
- [Auth](03-auth.md) - authentication settings and per-key access limitations
- [Cache](04-cache.md) - cache storages and caching policies
- [Upstream](05-upstream-config.md) - upstream blockchain providers, failsafe, scoring, validators, and labels
- [Rate Limiting](06-rate-limiting.md) - request throughput control for upstream providers
- [App Storages](07-app-storages.md) - shared Redis/Postgres storage referenced by cache and rate limiting
- [Tor setup](07-tor-setup.md) - running nodecore behind Tor for `.onion` upstreams
- [Prometheus metrics](08-prometheus-metrics.md) - metrics catalog exposed on the metrics port
- [Integration](09-integration.md) - DRPC platform integration
- [Quorum](10-quorum.md) - signed-response quorum verification
- [Method specs](11-method-specs.md) - per-chain method definitions and how to extend them
- [gRPC API](12-grpc-server.md) - public gRPC API for querying upstream and chain state
- [Subscriptions](13-subscriptions.md) - subscription aggregation and locally-synthesized subscriptions

By default, nodecore looks for a configuration file named `./nodecore.yml` in the current directory. You can override this path by setting the `NODECORE_CONFIG_PATH` environment variable. For example, `NODECORE_CONFIG_PATH=/path/to/your/config make run`.

Method specs (the per-chain RPC behavior definitions) are embedded into the binary - see [Method specs](11-method-specs.md). Log output format is controlled by the `LOG_FORMAT` environment variable (`json` or `console`, default `console`), and log level by `LOG_LEVEL`.

## Minimum working configuration

To start nodecore, you only need to define the `upstream-config` section with at least one upstream provider. All other settings will fall back to their default values.

The example below defines two upstreams (Ethereum and Polygon), each using a standard JSON-RPC connector.

```yaml
upstream-config:
  upstreams:
    - id: my-super-upstream
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://path-to-eth-provider.com
    - id: my-super-upstream-2
      chain: polygon
      connectors:
        - type: json-rpc
          url: https://path-to-polygon-provider.com
```

> **Supported chains and protocols**: nodecore supports the chains defined in [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml). Current chain families are EVM (Ethereum, Polygon, Optimism, Arbitrum, Base, BSC, Fantom, Linea, Mantle, Scroll, zkSync, and others), Solana, Algorand, Aztec, and Aptos. Supported upstream protocols are `json-rpc`, `websocket`, `rest`; the exact set available for each chain is declared by the chain's method spec (see [Method specs](11-method-specs.md)).

## Full configuration

To configure all aspects of nodecore, you can use the following example, which demonstrates every available section.

```yaml
server:
  port: 9090
  grpc-port: 9091
  metrics-port: 9093
  pprof-port: 6061
  tls:
    enabled: true
    certificate: /path
    key: /path
  pyroscope-config:
    enabled: true
    url: pyrosope-url
    username: pyro-username
    password: pyro-password
    additional-tags:
      env: prod
      region: eu-west-1
  grpc-auth:
    enabled: true
    public-key-owner: drpc
    provider-private-key-path: /path/to/provider.key
    external-public-key-path: /path/to/external.pub
    session-ttl: 24h

app-storages:
  - name: redis-storage
    redis:
      full-url: "redis://localhost:6379/0"
      address: "localhost:6379"
      username: username
      password: password
      db: 2
      timeouts:
        connect-timeout: 1s
        read-timeout: 2s
        write-timeout: 3s
      pool:
        size: 35
        pool-timeout: 5s
        min-idle-conns: 10
        max-idle-conns: 50
        max-active-conns: 45
        conn-max-idle-time: 60s
        conn-max-life-time: 60m
  - name: postgres-storage
    postgres:
      url: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable

cache:
  receive-timeout: 500ms
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
        cache-table: "cache"
        expired-remove-interval: 10s
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

auth:
  enabled: true
  request-strategy:
    type: token
    token:
      value: "my-token"
  #    type: jwt
  #    jwt:
  #      public-key: /path/to/key
  #      allowed-issuer: "my-iss"
  #      expiration-required: true
  key-management:
    - id: "my-first-key"
      type: local
      local:
        key: "bXkta2V5"
        settings:
          allowed-ips:
            - "192.0.0.1"
            - "127.0.0.1"
          methods:
            allowed:
              - "eth_getBlockByNumber"
            forbidden:
              - "eth_syncing"
          contracts:
            allowed:
              - "0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"

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
      - name: budget-override
        storage: redis-storage
        config:
          rules:
            - method: eth_blockNumber
              requests: 100
              period: 1s
  - default-engine: redis-storage
    budgets:
      - name: redis-budget
        config:
          rules:
            - method: eth_call
              requests: 200
              period: 1s

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
        enable:
          - "my_method"
        disable:
          - "eth_getBlockByNumber"
      rate-limit:
        rules:
          - method: eth_call
            requests: 200
            period: 1s
      failsafe-config:
        retry:
          attempts: 10
          delay: 2s
          max-delay: 5s
          jitter: 3s
    - id: auto-tune-upstream
      chain: ethereum
      rate-limit-auto-tune:
        enabled: true
        period: 1m
        error-threshold: 0.1
        init-rate-limit: 100
        init-rate-limit-period: 1s
      connectors:
        - type: json-rpc
          url: https://path-to-eth-provider-2.com

integration:
  drpc:
    url: https://drpc-integration-endpoint
    request-timeout: 10s
```
