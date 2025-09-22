# nodecore Configuration guide

The configuration file is the entry point for all nodecore settings. It is organized into several sections, each responsible for a specific part of the system:

* [Server](02-server-config.md) - configure the basic runtime settings of the nodecore server
* [Auth](03-auth.md) - manage authentication settings and access limitations
* [Cache](04-cache.md) - define cache storages and caching policies
* [Upstream](05-upstream-config.md) - configure upstream blockchain providers and their settings

By default, nodecore looks for a configuration file named `./nodecore.yml` in the current directory. You can override this path by setting the `NODECORE_CONFIG_PATH` environment variable. For example, `NODECORE_CONFIG_PATH=/path/to/your/config make run`.

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

> **⚠️ Important note**: Currently, nodecore supports Solana and Ethereum-compatible chains as defined in the [chains.yaml](https://github.com/drpcorg/public/blob/main/chains.yaml) file. Future releases will extend support to all chains listed in that file, along with additional protocols such as REST and gRPC.

## Full configuration

To configure all aspects of nodecore, you can use the following example, which demonstrates every available section.

```yaml
server:
  port: 9090
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
              - 'eth_getBlockByNumber'
            forbidden:
              - "eth_syncing"
          contracts:
            allowed:
              - "0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"

upstream-config:
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