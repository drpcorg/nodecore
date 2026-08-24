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
```

## Fields

- `port` - The main HTTP port where nodecore listens for incoming RPC requests. **_Default_**: `9090`
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
