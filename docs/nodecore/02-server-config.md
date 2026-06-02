# Server config guide

The `server` section controls how nodecore runs as a service: listening ports, TLS configuration, optional profiling/observability, and the gRPC API.

```yaml
server:
  port: 9090
  grpc-port: 9091
  metrics-port: 9093
  pprof-port: 6061
  tor-url: localhost:9050
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
- `metrics-port` - Port exposing Prometheus metrics (endpoint `GET /metrics`). By default, it's disabled, so it's necessary to specify the port explicitly to enable prom metrics
- `pprof-port` - Port for Go [pprof](https://github.com/google/pprof) profiling endpoints. By default, profiling is disabled; to enable it, you must explicitly set this port
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

## Environment variables

These behaviors are controlled by environment variables rather than the YAML config:

- `NODECORE_CONFIG_PATH` - path to the YAML config file. **_Default_**: `./nodecore.yml`
- `LOG_FORMAT` - log output format. Allowed values: `json` (structured JSON to stdout, suitable for log shippers) or `console` (human-readable to stderr). **_Default_**: `console`
- `LOG_LEVEL` - log level (e.g. `debug`, `info`, `warn`, `error`). Defaults are set by the logger package
