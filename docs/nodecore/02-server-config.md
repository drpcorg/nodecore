# Server config guide

The `server` section controls how nodecore runs as a service: listening ports, TLS configuration, optional profiling/observability, and the gRPC API.

```yaml
server:
  port: 9090
  grpc-port: 9091
  grpc-ingress-port: 9095
  metrics-port: 9093
  pprof-port: 6061
  health-port: 9096
  tor-url: localhost:9050
  trusted-proxies:
    - 10.0.0.0/8
    - 192.168.1.10
  routing-headers: false
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
  limits:
    max-request-size: 32MB
    max-batch-size: 1000
```

## Fields

- `port` - The main HTTP port where nodecore listens for incoming RPC requests. Request and response bodies on this port are gzip/zstd/brotli [compressed](15-compression.md) by content negotiation; there is nothing to configure. **_Default_**: `9090`
- `grpc-port` - Port exposing the [gRPC API](12-grpc-server.md) for querying upstream/chain state. Disabled by default; set explicitly to enable
- `grpc-ingress-port` - Port exposing the [gRPC chain ingress](14-grpc-ingress.md): native gRPC chain traffic (e.g. Sui's `sui.rpc.v2`) routed through the same execution flow as HTTP requests. Disabled by default. This is a **separate server** from `grpc-port` — different clients and auth models — reusing the same `tls` config; either can run without the other
- `metrics-port` - Port exposing Prometheus metrics (endpoint `GET /metrics`). By default, it's disabled, so it's necessary to specify the port explicitly to enable prom metrics
- `pprof-port` - Port for Go [pprof](https://github.com/google/pprof) profiling endpoints. By default, profiling is disabled; to enable it, you must explicitly set this port
- `health-port` - Port exposing operational health endpoints. **_Default_**: `9096`
- `pyroscope-config` - Optional integration with [Pyroscope](https://pyroscope.io/) for continuous profiling
  - `enabled` - Enable/disable Pyroscope integration. **_Default_**: `false`
  - `url`: URL of the Pyroscope server. **_Required_** if `enabled: true`
  - `username`, `password`: authentication credentials. **_Required_** if `enabled: true`
  - `additional-tags` - a string-to-string map of extra labels attached to every Pyroscope profile (e.g. `env: prod`)
- `tls` - TLS configuration for serving requests securely
  - `enabled` - whether TLS is enabled. **_Default_**: `false`
  - `certificate` - Path to the TLS certificate file. **_Required_**
  - `key` - Path to the TLS private key file. **_Required_**
  - `ca` - Path to a Certificate Authority (CA) certificate file to validate client certificates
- `grpc-auth` - Signature-based authentication for the gRPC API. See [gRPC API](12-grpc-server.md) for the full handshake model
  - `enabled` - whether gRPC auth is required. **_Default_**: `false`
  - `public-key-owner` - identifier of the entity that owns the external public key (used in logs and audit trails). **_Default_**: `drpc`
  - `provider-private-key-path` - filesystem path to nodecore's own private key used to sign session responses. **_Required_** if `enabled: true`; the file must exist
  - `external-public-key-path` - filesystem path to the public key used to verify incoming client signatures. **_Required_** if `enabled: true`; the file must exist
  - `session-ttl` - lifetime of a successful authentication session before a new handshake is required. **_Default_**: `24h`
- `tor-url` - Address of a SOCKS5 proxy (typically a local Tor instance) used for connecting to `.onion` upstreams. Format: `host:port`. Example: `localhost:9050`. See [Upstream Config](05-upstream-config.md#tor-onion-upstreams) for details
- `trusted-proxies` - A list of reverse proxies/load balancers in front of nodecore, as CIDRs (`10.0.0.0/8`) or bare IPs (`192.168.1.10`, treated as `/32` or `/128`). Controls whether the `X-Forwarded-For` header is trusted when resolving the client IP for [key `allowed-ips` checks](03-auth.md#local-keys). Invalid entries fail config validation at startup. **_Default_**: empty. See [Client IP resolution](#client-ip-resolution) below
- `routing-headers` - Lets clients restrict a request to a group label or to named upstreams with the `X-Nodecore-Group-Label` and `X-Nodecore-Upstream-Id` headers. **_Default_**: `false`. See [Routing headers](#routing-headers) below
- `limits` - Optional bounds on what a single client request may ask of nodecore. Nothing is limited unless a field is set. See [Request limits](#request-limits) below
  - `max-request-size` - The largest request nodecore reads: one HTTP body, or one WebSocket message. A positive integer followed by `KB` or `MB` (`512KB`, `32MB`). The units are binary: `1KB` is 1024 bytes and `1MB` is 1024 KB, so `32MB` is 32 MiB. **_Default_**: not set (no limit)
  - `max-batch-size` - The largest number of items in one JSON-RPC batch, over HTTP and WebSocket. `0` means no limit, the same as leaving the field out. **_Default_**: not set (no limit)


## Client IP resolution

The client IP is used for the `allowed-ips` check of [local](03-auth.md#local-keys) and [DRPC](03-auth.md#drpc-keys) keys. How it is resolved depends on `trusted-proxies`:

**`trusted-proxies` is set (recommended when nodecore runs behind a proxy).** Exactly one client IP is resolved:

1. If the direct peer of the TCP connection is **not** listed in `trusted-proxies`, that peer is the client IP and `X-Forwarded-For` is ignored entirely - a client connecting directly cannot spoof its IP with that header.
2. If the direct peer **is** a trusted proxy, the `X-Forwarded-For` chain (all values of the header, in wire order) is walked from right to left and the first entry that is not itself a trusted proxy becomes the client IP. Because a proxy appends the IP it received the connection from, entries that an attacker prepended are ignored.
3. If every forwarded entry is a trusted proxy, or the header is absent, the direct peer is used.

Entries are accepted both as a bare IP and in the `ip:port` form that some gateways (for example Azure Application Gateway) append; the port is discarded. An entry that is neither is skipped, and the walk continues to its left.

**`trusted-proxies` is empty (the default).** The legacy behavior is preserved for backwards compatibility: every `X-Forwarded-For` entry is treated as a candidate client IP, and the direct peer is used only when the header is absent.

> [!WARNING]
> In the default (empty) mode a client that connects directly to nodecore can present an arbitrary IP by sending its own `X-Forwarded-For` header, which is enough to satisfy an `allowed-ips` restriction. If you rely on `allowed-ips`, set `trusted-proxies` to the proxies actually in front of nodecore; if there are none, list `127.0.0.1` so that `X-Forwarded-For` from remote clients is never honored.

If the peer address cannot be parsed as an IP, `127.0.0.1` is used.

## Request limits

The `limits` section is off by default: with no section, or with a field left out, nodecore behaves as if the limit did not exist. Both limits apply to client traffic on `port` (HTTP and WebSocket); the gRPC servers are not affected. A change to the section takes effect on restart.

**`max-request-size`** bounds a request as nodecore reads it.

- An HTTP body over the limit is answered with `413` and the message `request body is too large`. A request that declares a `Content-Length` over the limit is rejected without reading its body.
- A [compressed](15-compression.md) body is bounded twice: by its size on the wire, as sent, and by the size it decodes to. Neither may exceed the limit, so a body that is larger compressed than decoded - data that does not compress - is measured by its compressed size.
- When `max-request-size` is set it replaces the built-in 32 MiB cap on decoded bodies, so a value above 32 MiB raises that cap and a value below it lowers it. When it is not set, the built-in cap keeps applying to the decoded size of compressed bodies, and nothing bounds a body on the wire.
- A WebSocket message over the limit closes the connection with the `1009` ("message too big") close code.
- A body or message of exactly the limit is served.

**`max-batch-size`** bounds the number of items in a JSON-RPC batch. A batch with more items is rejected as a whole, before any of its items is sent to an upstream, with one error:

```json
[{"id":0,"jsonrpc":"2.0","error":{"message":"client error - batch size 1500 exceeds the limit of 1000","code":400}}]
```

Over HTTP the error is the only item of the response array, as above. Over WebSocket, where every item of a batch is answered with a message of its own, the rejection is one message holding the error object alone, without the array around it:

```json
{"id":0,"jsonrpc":"2.0","error":{"message":"client error - batch size 1500 exceeds the limit of 1000","code":400}}
```

The connection stays open. A single (non-batch) request is never affected.

A batch is parsed before its items are counted, so `max-batch-size` bounds the work a batch sends to upstreams, not the memory it takes to parse. Set `max-request-size` next to it to bound that as well.

## Routing headers

By default a client has no say in which upstream serves its request: the chain's balancing strategy and, for a key, its [upstream restriction](03-auth.md#upstream-restriction) decide. With `routing-headers: true` a client may narrow one request itself with two headers:

| Header | Value | Admits |
| --- | --- | --- |
| `X-Nodecore-Group-Label` | one label | upstreams whose [`group-labels`](05-upstream-config.md#label-balancing) contain it |
| `X-Nodecore-Upstream-Id` | one or more upstream ids | upstreams whose `id` is one of them |

```bash
# serve this request from the "archive" group and nowhere else
curl 'http://localhost:9090/queries/ethereum' \
  -H 'Content-Type: application/json' \
  -H 'X-Nodecore-Group-Label: archive' \
  -d '{"id":1,"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0xf4240"}]}'

# send this request to one named upstream
curl 'http://localhost:9090/queries/ethereum' \
  -H 'Content-Type: application/json' \
  -H 'X-Nodecore-Upstream-Id: own-archive' \
  -d '{"id":1,"jsonrpc":"2.0","method":"eth_blockNumber"}'
```

How the values are read:

- `X-Nodecore-Group-Label` is one label, taken as written. It is never split: `archive, full` is a single label that no upstream carries.
- `X-Nodecore-Upstream-Id` takes several ids, comma-separated (`a, b`) or as repeated header lines; any of the named upstreams may serve. An upstream whose id contains a comma cannot be named.
- An empty header is treated as not sent.

How a request with a header is routed:

- The headers are **filters with no fallback**. An upstream they do not admit is never used for the request - not on the first attempt, not on a retry, not for a hedge. If no admitted upstream can serve, the request fails with `no available upstreams`. With one upstream admitted there is nothing to retry on or hedge to: its answer, or its error, is the response.
- When both headers are sent, an upstream must satisfy both.
- The request is **not routed by [`label-balancing`](05-upstream-config.md#label-balancing)**: group order, `pass-on-error` and `include-default` do not apply to it. The chain's [`balancing-strategy`](05-upstream-config.md#balancing-strategy) orders the admitted upstreams. Naming several ids says any of them may serve; it promises no particular split of requests between them.
- Everything else still applies to an admitted upstream: it must be available, support the method and have rate-limit budget left. An upstream that cannot serve is an error, not an exception to these checks.
- A key's [upstream restriction](03-auth.md#upstream-restriction) still holds: the headers narrow within the key's upstreams and never widen them.
- The cache is shared, as for any request. A cached answer is returned without asking an upstream, and the `response-provider` response header then reads `NoUpstream`; on a single (non-batch) request served by an upstream it carries that upstream's id.
- In a batch the headers apply to every item.
- One exception, the same one the key restriction has: the local `newPendingTransactions` and `drpc_pendingTransactions` subscription sources merge every upstream of the chain and do not honor the headers. See the warning under [Upstream restriction](03-auth.md#upstream-restriction).

Where the headers are read:

- **HTTP** (JSON-RPC and REST), per request.
- **WebSocket**, from the handshake request: the restriction is fixed for the connection and applies to every request and subscription on it. Subscriptions restricted differently never share an upstream subscription. A browser's WebSocket API cannot set custom request headers, so the headers are usable only from WebSocket clients that can, such as server-side ones.
- **[gRPC chain ingress](14-grpc-ingress.md)**, as the metadata keys `x-nodecore-group-label` and `x-nodecore-upstream-id`.

None of these ever forwards the two headers to an upstream, whether the option is on or off. The option is read at startup; a change takes effect on restart.

> **⚠️ The headers override the routing you configured.** Once the option is on, any client that can send a request can choose a group you ordered last in `label-balancing`, or keep all of its traffic on one node. Leave it off when nodecore is reachable by callers you do not control.

## Request parsing

A request whose method name is not valid UTF-8 is rejected during parsing.

## Health endpoints

When `health-port` is configured, nodecore exposes lightweight Kubernetes-friendly health endpoints on that port:

- `GET /health` - liveness probe. It intentionally returns `200 OK` whenever the process and health HTTP server are alive. It does **not** check upstreams or external dependencies, so Kubernetes can use it to decide whether to restart the container without restarting healthy pods during upstream/network incidents.
- `GET /ready` - readiness probe. It returns `200 OK` when at least one chain supervisor is currently available; otherwise it returns `503 Service Unavailable`. Use this endpoint to decide whether the pod should receive traffic.
- `GET /status` - diagnostic JSON endpoint with the same readiness boolean plus per-chain statuses. This is intended for operators and monitoring dashboards, not for Kubernetes liveness decisions.

## Environment variables

These behaviors are controlled by environment variables rather than the YAML config:

- `NODECORE_CONFIG_PATH` - path to the YAML config file. **_Default_**: `./nodecore.yml`
- `LOG_FORMAT` - log output format. Allowed values: `json` (structured JSON to stdout, suitable for log shippers) or `console` (human-readable to stderr). **_Default_**: `console`
- `LOG_LEVEL` - log level (e.g. `debug`, `info`, `warn`, `error`). Defaults are set by the logger package
