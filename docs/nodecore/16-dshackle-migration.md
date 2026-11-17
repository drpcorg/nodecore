# Migrating a dshackle config

nodecore ships a converter that turns a dshackle config file into a nodecore one. It carries over
what has a counterpart in nodecore, and prints a report of everything it left out or changed, so
that nothing is dropped silently.

The converter only writes a config file. It does not start nodecore, and it does not touch the
dshackle config.

## Running the converter

The converter is a Go program in this repository, so you need a checkout and the Go toolchain.

```bash
make convert-dshackle IN=/path/to/dshackle.yaml OUT=/path/to/nodecore.yml
```

- `IN` is the dshackle config, `OUT` is the nodecore config to write.
- The converter refuses to overwrite an existing `OUT` file. Add `FORCE=1` to allow it; any other
  value, `FORCE=0` included, does not.
- The report goes to stderr. To keep it, redirect it: `make convert-dshackle IN=... OUT=... 2> report.txt`.
- The exit code is `0` when a file was written and `1` otherwise. On failure nothing is written.
- The output file is created with mode `0600`, because it can hold credentials copied from the
  input. If nodecore runs under another user (the Docker image runs as uid `65532`), change the
  owner or the mode before starting it.

The same program can be run directly: `go run ./cmd/dshackle-convert -in <file> -out <file> [-force]`.

Every converted upstream, and then the whole output, is checked with nodecore's own config
validation before the file is written, so the keys, values and connector types of the output are
ones nodecore accepts. Two things the converter cannot check, and nodecore will not start until
they are right:

- [`${VAR}` placeholders](#var-placeholders) in URLs.
- [Files referenced by path](#files-referenced-by-path), such as the gRPC auth keys, which have to
  exist where nodecore runs.

## What the output always contains

These values are written regardless of the input:

| Setting | Value | Why |
| --- | --- | --- |
| `upstream-config.mode` | `strict` | The profile closest to dshackle: every validator runs and the head is tracked over WebSocket when the upstream has one. See [mode](05-upstream-config.md#mode) |
| `upstream-config.balancing-strategy` | `base` | See [balancing-strategy](05-upstream-config.md#balancing-strategy) |
| `server.grpc-port` | dshackle's `port`, or `2449` | nodecore's gRPC server is off unless a port is set |
| `server.metrics-port` | `monitoring.prometheus.port`, or `8081` | dshackle serves metrics by default, also without a `monitoring` section. Left out when dshackle's metrics are disabled |

`server.port`, nodecore's HTTP port, is not written; nodecore opens it on its default `9090`.

## Reading the report

The report starts with a summary line and has up to three groups:

```
converted 3 of 6 upstreams -> nodecore.yml

skipped upstreams:
  xrp-1: chain 'ripple' is not served by nodecore
  remote-eu: dshackle-to-dshackle upstreams (connection.grpc) are not supported

needs manual action:
  connection.generic.rpc.url (sol-1): contains a ${VAR} placeholder; nodecore does not substitute environment variables, replace it with the value (nodecore will not start until it is replaced)

not carried over:
  proxy: nodecore has no route-id HTTP proxy; its HTTP endpoint is /queries/<chain> on server.port
  role (eth-2): roles are not supported; the upstream is treated as primary
```

| Group | Meaning |
| --- | --- |
| `skipped upstreams` | The upstream is not in the output at all. The line gives the reason |
| `needs manual action` | Something you have to fix or decide before using the file |
| `not carried over` | A setting with no place in a nodecore config. Nothing to fix, but the behaviour it configured is gone |

A line names the dshackle key and, in brackets, the upstreams it applies to. A setting present on
many upstreams is reported once, with all their ids.

Any key the converter does not know, or whose value it cannot use, appears under `not carried over`
as `not converted (unknown key or unusable value)`.

## What is converted

### Server settings

| dshackle | nodecore |
| --- | --- |
| `port` | `server.grpc-port` |
| `tls.enabled`, `tls.server.certificate`, `tls.server.key` | `server.tls.enabled`, `certificate`, `key` |
| `auth.enabled`, `auth.publicKeyOwner` | `server.grpc-auth.enabled`, `public-key-owner` |
| `auth.server.keys.provider-private-key`, `external-public-key` | `server.grpc-auth.provider-private-key-path`, `external-public-key-path` |
| `monitoring.prometheus.port` | `server.metrics-port` |
| `health.port` | `server.health-port` (`8082` when the section has no port) |

The key and certificate files themselves are not copied; the paths are. The same files work with
nodecore.

A `tls` or `auth` section that is switched off in dshackle is written switched off, with the paths
it holds, so you only have to flip `enabled` to turn it on. A section that is on but incomplete
(TLS without both certificate and key, auth without both key paths) is not written and is reported
under `needs manual action`.

### Upstreams

| dshackle | nodecore |
| --- | --- |
| `id` | `id` |
| `chain` | `chain`, in nodecore's canonical spelling (`ETHEREUM` and `eth` both become `ethereum`) |
| `labels` | `labels` |
| `custom-headers` | `headers` on the connectors made from `rpc` and `ws` |
| `methods.enabled`, `methods.disabled` | `methods.enable`, `methods.disable` |
| `method-groups.enabled`, `method-groups.disabled` | group names in `methods.enable`, `methods.disable` |
| `additional-settings.manual-lower-bounds` with `type: fixed` | `lower-bounds` |
| `options` | `options`, see [Options](#options) |
| `cluster.defaults` | `upstream-config.chain-defaults` |

### Connectors

dshackle puts every endpoint under `rpc` or `ws` and decides the protocol from the chain. nodecore
wants the connector type spelled out, so the converter picks it:

| dshackle | nodecore connector |
| --- | --- |
| `rpc.url` on the Beacon Chain, Algorand or TON | `rest` |
| `rpc.url` on a Cosmos SDK chain | `tendermint` |
| `rpc.url` on any other chain | `json-rpc` |
| `ws.url` | `websocket` |
| `connection.bitcoin.esplora.url` | `rest-additional` on the same upstream; it serves `listunspent` |
| `additional[]` entry with `tag: ton_v3` on TON | `rest-indexer` on a second upstream, see below |

`basic-auth` and `bearer-auth` become an `Authorization` header on that connector, `ws.origin`
becomes an `Origin` header, and `tls.ca` becomes the connector's `ca`.

**TON.** dshackle describes a TON node and its v3 indexer as one upstream. nodecore recommends one
upstream per API (see [TON deployment modes](05-upstream-config.md#ton-deployment-modes)), so such
an upstream is split in two: `<id>` with a `rest` connector and `<id>-v3` with a `rest-indexer`
connector. Labels, options and disabled methods go to both. The report says so, because `<id>-v3` is
a new upstream id.

**`connector-mode`.** nodecore has no connector modes. With both `rpc.url` and `ws.url` it sends
requests over HTTP and, in `strict` mode, tracks the head over WebSocket, which matches
dshackle's default `RPC_REQUESTS_WITH_WS_HEAD`. `RPC_ONLY` drops the `ws.url`. `WS_ONLY` with an
`rpc.url`, and `RPC_REQUESTS_WITH_MIXED_HEAD`, cannot be expressed and are reported.

### Options

Only options present in the input are written; everything else falls back to the defaults of
`strict`.

| dshackle option | nodecore option |
| --- | --- |
| `validate-syncing`, `validate-peers`, `min-peers`, `validate-call-limit`, `call-limit-size`, `disable-validation`, `disable-log-index-validation`, `disable-liveness-subscription-validation` | same name, same value |
| `validation-interval: 30` (seconds) | `validation-interval: 30s` |
| `validate-chain: false` | `disable-chain-validation: true` |
| `disable-upstream-validation` | `disable-settings-validation` |
| `disable-bound-validation` | `disable-lower-bounds-detection` |
| `min-peers: 0` | `validate-peers: false` (nodecore reads a zero `min-peers` as "not set") |

`min-peers` and `validate-peers` are converted as a pair across `cluster.defaults` and the
upstream. An upstream that raises `min-peers` above a zero default also gets `validate-peers: true`,
so that peers are checked again as they are in dshackle.

## What is not converted

| dshackle | What happens |
| --- | --- |
| `proxy` | Reported. nodecore has no route-id HTTP proxy; clients use `/queries/<chain>` on `server.port` |
| `cache`, `tokens`, `accessLog`, `compression`, `passthrough` | Reported. Configure the nodecore counterpart by hand where one exists, for example [cache](04-cache.md) |
| `cluster.include` | Reported. Only the file given as `IN` is converted. To convert the included upstreams too, move them into `cluster.upstreams` of the main file first |
| Upstreams with `connection.grpc` | Skipped. nodecore does not connect to another dshackle |
| Upstreams with `enabled: false` | Skipped |
| Upstreams on a chain nodecore does not serve | Skipped |
| `tls.client` | Reported. nodecore does not verify client certificates |
| `rpc.tls.certificate`, `rpc.tls.key` | Reported. nodecore does not present a client certificate to a node |
| `role: secondary` or `fallback` | Reported. Every upstream is treated as primary |
| `methods.enabled[].quorum`, `methods.enabled[].static` | Reported. A method with a static response is not enabled |
| Lower bounds with `type: head` | Reported. nodecore bounds are fixed block numbers |
| `connection.bitcoin.zeromq` | Reported. nodecore polls the head over `rpc` |
| `node-id`, `rpc.max-connections`, `rpc.queue-size`, `ws.frameSize`, `ws.msgSize`, `ws.connections` | Reported. No counterpart |
| `validate-gas-price`, `disable-pending-tx-validation`, `balance` | Reported. No counterpart |
| `timeout` | Reported. dshackle reads this option but never uses it, so there is nothing to carry over |
| `health.path`, `health.blockchains` | Reported. The health endpoints are fixed, see [Health endpoints](02-server-config.md#health-endpoints) |
| `monitoring.prometheus.path` | Reported when it is not `/metrics`; the path is fixed |
| `chain-settings`, `version` | Ignored without a report line |

## What to check by hand

### `${VAR}` placeholders

dshackle replaces `${NAME}` in any string value with an environment variable. nodecore does not.
The converter copies such a value as it is and reports it under `needs manual action`; replace it
with the real value in the output file.

- A placeholder in the host or port of a URL, or one that is the whole URL, makes the URL invalid.
  The upstream is still written, and the report says nodecore will not start until it is replaced.
- A placeholder inside `basic-auth` cannot be encoded into a header. The `Authorization` header is
  not written, and the report tells you to add `Authorization: Basic <base64(username:password)>`.

### Files referenced by path

The converter copies paths, not files: the TLS certificate and key, the gRPC auth keys, and a
connector's `ca`. The same files work with nodecore, but they have to exist at the written paths on
the host or in the container where nodecore runs.

nodecore checks the two gRPC auth key files at startup and refuses to start when one is missing.
The converter cannot run that check, so an enabled `grpc-auth` section is always listed under
`needs manual action`.

### Listen addresses

dshackle binds its gRPC, metrics and health listeners to `127.0.0.1` unless told otherwise.
nodecore listens on all interfaces. When dshackle's address was not `0.0.0.0` or `::`, the report
warns that the port becomes reachable from outside; close it with a firewall or the container's
port mapping if that is not wanted.

### Endpoints a dshackle config cannot describe

nodecore supports more connector types than a dshackle config can express. The converter cannot
invent these endpoints, so add them by hand if your nodes serve them:

| Chain | Connector | What it adds |
| --- | --- | --- |
| Cosmos SDK chains | `rest` | The LCD / gRPC-gateway API |
| Cosmos SDK chains, TRON | `grpc` | The node's gRPC API |
| TRON | `rest`, `rest-additional`, `grpc-additional` | The `/wallet/*` API, the solidity mirror, the solidity gRPC port |
| Hyperliquid | `rest-additional` | The REST methods next to the EVM API |

See [connectors](05-upstream-config.md#connectors) for each type.

## Known limits

- **A `ws.url` on a chain without WebSocket support.** nodecore accepts a `websocket` connector only
  on chains whose method spec declares one. On other chains (for example most Cosmos SDK chains,
  NEAR, Starknet, TRON, the Beacon Chain, TON and Algorand) an upstream with a `ws.url` is rejected
  by validation and skipped. Remove the `ws` block from the dshackle config and convert again.
- **Method groups.** In dshackle the `trace` and `debug` groups are off unless enabled. nodecore
  starts from every method of the chain's spec and, in `strict` mode, removes the ones the
  node does not serve. The converter does not add `disable: [trace, debug]` on its own.
- **A method group the chain does not have** (for example `trace` on Solana) is not written and is
  reported.
