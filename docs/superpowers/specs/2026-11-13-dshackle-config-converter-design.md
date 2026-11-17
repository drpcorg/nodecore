# dshackle config converter

**Date:** 2026-11-13
**Branch:** `dshackle-script`
**Status:** approved 2026-11-16; not implemented

## 1. Problem

An operator who runs dshackle and wants to move to nodecore has to rewrite `dshackle.yaml` as
`nodecore.yml` by hand. The two formats describe the same things (listening ports, TLS, auth
keys, a list of upstreams with options) under different keys and with different rules, and
nodecore needs information dshackle derives on its own, most importantly the connector type
of every endpoint. Hand conversion is slow and easy to get wrong, and the operator has no way
to tell which dshackle settings have no nodecore equivalent.

## 2. Goal and non-goals

**Goal.** A Go program in this repo that reads one dshackle config file and writes a nodecore
config file that nodecore accepts, and reports every input setting it did not carry over.

**Non-goals.**

- No change to how nodecore behaves or to its config schema. The one edit outside the
  converter is `omitempty` on the yaml tags of the config structs the converter writes
  (decision 13); parsing ignores it and nothing in nodecore marshals those structs.
- No new nodecore features to close gaps (client-certificate verification, environment
  variable substitution, per-chain health requirements, an HTTP proxy with route ids). The
  converter reports these; it does not work around them.
- Files named in `cluster.include` are not read.
- `chain-settings` is not converted.
- No cut-over or rollback tooling.
- The migration guide (section 9) is a separate deliverable and is not part of this work.

**Anti-goals.** The converter must never write a file that nodecore rejects at startup, with
one agreed exception: a URL that still holds a `${VAR}` placeholder (5.9), which the report
lists for manual replacement. And it must never drop an input setting without saying so (the
two agreed exceptions are in 6.1).

## 3. Approaches

| Approach | Trade-off | Verdict |
|---|---|---|
| Shell script | Needs `yq`; base64 headers, chain lookups and a warning report are awkward | Rejected |
| Node script (`scripts/*.mjs`) | Matches the existing scripts, but needs a YAML dependency and its own copy of the chain registry, which goes stale; cannot run nodecore's validation | Rejected |
| Go, as a subcommand of the nodecore binary | Ships in the image, nothing to install; puts converter code into the server binary | Rejected |
| **Go, separate `cmd/dshackle-convert` + make target** | Uses the real chain registry, method specs and config validation; needs a repo checkout and Go to run | **Chosen** |

The deciding argument was validation: only a Go program in this module can run nodecore's
own config validation on what it generates.

## 4. Key decisions

| # | Decision | Source |
|---|---|---|
| 1 | Lives in `cmd/dshackle-convert/`, run through `make convert-dshackle`; nothing added to the nodecore binary or image | user |
| 2 | Output always sets `upstream-config.mode: strict` and `upstream-config.balancing-strategy: base` | user |
| 3 | Every input setting that is not carried over produces a warning | user |
| 4 | `chain-settings` is ignored without a warning | user |
| 5 | `cluster.include` is not followed; one warning says only the given file was converted | user |
| 6 | A `${VAR}` placeholder is copied as is, with a warning. An upstream is kept even when the placeholder makes its URL unparsable for nodecore (5.9) | user |
| 7 | `proxy` is not converted (warning only) | user |
| 8 | `tls.client.*` is not converted (warning only); no nodecore work on client certificates | user |
| 9 | The generated config is checked with nodecore's own validation before it is written | user |
| 10 | Connector type is derived from the chain's family in nodecore's registry (5.4) | mine |
| 11 | An upstream nodecore's validation rejects is left out of the output and reported, instead of failing the whole run | mine |
| 12 | Validation is called through the existing `config.LoadReloadable(doc, doc)`, so no new entry point is added to `internal/config` | mine |
| 13 | The output is built from nodecore's own config structs (`config.AppConfig` and below), not from structs of the converter's own, so a key that is renamed or retyped in nodecore is followed by the compiler instead of by hand. For that the yaml tags of those structs carry `omitempty` (5.11) | user |
| 14 | Any input key the converter has no rule for is reported by a generic catch-all warning | mine |
| 15 | Chain names are resolved the way dshackle resolves them and written in nodecore's canonical spelling, on upstreams and in `chain-defaults` alike (5.4) | mine |
| 16 | A TON upstream that has a `ton_v3` endpoint is split into two nodecore upstreams, one per API (5.4), the layout nodecore recommends for TON | user |
| 17 | `metrics-port` is emitted even when the input has no `monitoring` section, because dshackle serves metrics on `8081` by default | mine |
| 18 | `method-groups` is carried over as group names in `methods.enable` / `methods.disable` (5.4). Nothing is disabled to imitate dshackle's "trace and debug are off unless enabled" default | user |
| 19 | A `tls` or `auth` section that is switched off is carried over switched off, with its paths, instead of being left out (5.3) | user |
| 20 | `connection.bitcoin.esplora` becomes a `rest-additional` connector on the same upstream (5.4); nodecore serves `listunspent` from it, as dshackle does | user |

## 5. Design

### 5.1 Usage

```
make convert-dshackle IN=/path/dshackle.yaml OUT=/path/nodecore.yml
```

The target depends on `generate-networks` (the chain registry file `pkg/chains/chains_data.go`
is generated and gitignored) and runs
`go run ./cmd/dshackle-convert -in $(IN) -out $(OUT)`. `FORCE=1` adds `-force`; any other value
of `FORCE`, `0` included, does not.

Flags: `-in` (required), `-out` (required), `-force` (overwrite an existing `-out` file;
without it an existing file is an error, because the repo root holds a checked-in
`nodecore.yml`).

The converted config goes to the `-out` file. The report goes to stderr.

Exit code `0`: a file was written, with or without warnings. Exit code `1`, nothing written:
the input cannot be read or parsed, no upstream survived conversion, the `-out` file exists
without `-force`, or the final validation failed.

### 5.2 Flow

1. Load the method specs (`specs.NewMethodSpecLoader().Load()`, as `cmd/nodecore/main.go`
   does); connector validation needs them.
2. Parse the input, recording which keys the conversion rules read.
3. Convert the server-level settings (5.3).
4. Convert `cluster.defaults` and every entry of `cluster.upstreams` (5.4 to 5.7).
5. Validate each converted upstream on its own (5.8); drop and report the ones that fail.
6. Report every input key that no rule read (decision 14).
7. Validate the whole output (5.8). On failure print the error and exit `1`.
8. Write the output file, print the report.

### 5.3 Server-level settings

| dshackle | nodecore | Notes |
|---|---|---|
| `port` | `server.grpc-port` | always emitted; `2449` when absent (dshackle's default) |
| `tls.enabled`, `tls.server.certificate`, `tls.server.key` | `server.tls.enabled`, `certificate`, `key` | emitted whenever the `tls` section is present. dshackle turns TLS on when the section is there unless it says `enabled: false`; a section that is off is written with `enabled: false` and its paths (see "Disabled sections" below) |
| `tls.client.require`, `tls.client.ca`, `tls.client.cas` | none | warning: nodecore does not verify client certificates |
| `auth.enabled` | `server.grpc-auth.enabled` | emitted whenever the `auth` section is present; `false` when the key is absent, as in dshackle. A section that is off is written with `enabled: false` and what it holds (see "Disabled sections" below) |
| `auth.publicKeyOwner` | `server.grpc-auth.public-key-owner` | |
| `auth.server.keys.provider-private-key` | `server.grpc-auth.provider-private-key-path` | same key file works (PEM RSA) |
| `auth.server.keys.external-public-key` | `server.grpc-auth.external-public-key-path` | same key file works |
| an enabled `auth` section | "needs manual action" line | nodecore refuses to start unless both key files exist at the written paths, and this is the one check the converter cannot run (5.8) |
| `auth.client` | none | warning |
| `monitoring` | `server.metrics-port` | emitted unless `monitoring.enabled` or `prometheus.enabled` is `false`, also when the section is absent (dshackle serves metrics by default); value is `prometheus.port`, or `8081` |
| `monitoring.prometheus.path` other than `/metrics` | none | warning: the path is fixed to `/metrics` |
| `monitoring.jvm`, `extended`, `netty-metrics` | none | warning |
| `health` | `server.health-port` | emitted when the section is present; value is `health.port`, or `8082` (dshackle's default) |
| `health.path`, `health.blockchains` | none | warning: endpoints are `/health`, `/ready`, `/status`; per-chain `min-available` is not supported |
| `proxy`, `cache`, `tokens`, `accessLog`, `compression`, `passthrough` | none | one warning per section |
| `version`, `chain-settings` | none | ignored silently |

**Disabled sections.** A `tls` or `auth` section that is switched off in dshackle is still
carried over, switched off, with every path it holds. Leaving it out would drop configured
paths without a trace, and the operator would have no hint why the output has no TLS or auth.
nodecore checks the paths of these sections only when they are enabled, so a disabled section
is always accepted, also when a path is missing (the missing key is simply not written). To
turn the feature on the operator flips `enabled`.

The opposite case is unchanged: a section that is **on** but incomplete (TLS without both
certificate and key, auth without both key paths) is not written and gets a "needs manual
action" line, because nodecore would refuse it. An empty string counts as missing.

**Bind addresses.** nodecore listens on all interfaces. For each of the three listeners that
is emitted (gRPC `host`, `monitoring.prometheus.bind` or `host`, `health.host`), when
dshackle's effective address (the configured one, or its default `127.0.0.1`) is not
`0.0.0.0` or `::`, the converter warns that the port becomes reachable on all interfaces.

`server.port` (nodecore's HTTP port) is not emitted; nodecore opens it on its default `9090`.

### 5.4 Upstreams

An upstream is **skipped with a warning** when:

- `enabled: false`;
- `connection.grpc` (a dshackle-to-dshackle upstream);
- its `chain` does not resolve to a chain nodecore serves (see below);
- it has no `connection`, or the connection has neither `rpc.url` nor `ws.url`;
- nodecore's validation rejects the converted result (5.8).

**Chain names.** dshackle matches a chain name case-insensitively, treats `_` as `-`, and
accepts any alias of the chain. nodecore matches the name exactly, and applies
`chain-defaults` to an upstream only when the key is the very same string as the upstream's
`chain`. The converter therefore lowercases the name, replaces `_` with `-`, looks it up with
`chains.GetChain`, and writes the chain's first short name (`ShortNames[0]`) everywhere: as
the upstream's `chain` and as the `chain-defaults` key. So `chain: ETHEREUM` on an upstream
and `chains: [eth]` in `cluster.defaults` end up under the same name.

A chain is not served by nodecore when the lookup finds nothing, or when the chain has no
method spec (`MethodSpec == ""`). The second case is how Ripple and Kadena look: they are in
the registry but have no spec.

Field mapping:

| dshackle | nodecore | Notes |
|---|---|---|
| `id` | `id` | copied |
| `chain` | `chain` | canonical name, see above |
| `labels` | `labels` | copied; blank keys or values are dropped, as dshackle does |
| `custom-headers` | `headers` on every connector of the upstream | |
| `methods.enabled[].name` | `methods.enable` | |
| `methods.enabled[].quorum` | none | warning |
| `methods.enabled[].static` | none | the method is **not** enabled (nodecore would forward it to the node instead of answering with the static value); warning |
| `methods.disabled[].name` | `methods.disable` | |
| `method-groups.enabled`, `method-groups.disabled` | group names in `methods.enable`, `methods.disable` | see "Method groups" below |
| `additional-settings.manual-lower-bounds.<type>` with `type: fixed` | `lower-bounds.<type>: <value>` | `<type>` lowercased; both sides use the same set of names |
| the same with `type: head` | none | warning: nodecore bounds are static |
| `role` | none | `primary` and `standard` are silent; `secondary` and `fallback` get a warning |
| `node-id` | none | warning |
| `options` | `options` | see 5.6 |

**Method groups.** nodecore expands an entry of `methods.enable` or `methods.disable` that
names a group of the chain's method spec (`trace`, `debug`, `filter`, `default`, ...) to the
whole group, and applies `disable` before `enable`. dshackle's groups have the same names, so
each group goes to the matching list, ahead of the per-method names:

```yaml
# dshackle                      # nodecore
method-groups:                  methods:
  enabled: [trace]                enable: [trace]
  disabled: [default]             disable: [default]
```

dshackle builds the set of allowed methods in this order: the chain's default methods, plus
the enabled groups, minus the disabled groups, plus `methods.enabled`, minus
`methods.disabled`. Three of the four overlaps between the lists need care:

| Case | What the converter does |
|---|---|
| The group is not in the method spec of this chain for the upstream's connectors (`trace` on Solana) | Not written, warning. nodecore would otherwise take the name for a method called `trace` and enable it |
| A group listed as both enabled and disabled | Kept in `disable` only, warning. dshackle lets disabled win; nodecore's validation refuses the same name in both lists |
| A method in `methods.disabled` that belongs to an enabled group | Taken out of `disable`, warning that it stays on. In dshackle a per-method disable wins over an enabled group; in nodecore `enable` is applied last |
| A method in `methods.enabled` that belongs to a disabled group | Carried over as it is, no warning: the group goes to `disable`, the method to `enable`. The method is on in both systems, because dshackle adds `methods.enabled` after it removes the disabled groups and nodecore applies `enable` last |

One difference is left alone on purpose. In dshackle `trace` and `debug` are off unless their
group is enabled. nodecore starts from every method of the spec, and under `strict`
method detection removes what the node does not serve; an `enable` entry keeps a method on
regardless of detection. The converter does not add `disable: [trace, debug]` to an upstream
without those groups.

On a split TON upstream the disabled groups go to both halves, like `methods.disabled`.

**Connection.** `connection.generic`, `connection.ethereum` and
`connection.ethereum-pos.execution` are read the same way; `connection.bitcoin` contributes
its `rpc` and `esplora` blocks and never a `ws` one. Each endpoint becomes one entry of
`connectors`:

| dshackle | nodecore connector |
|---|---|
| `rpc.url` | type by chain family, below |
| `ws.url` | `websocket` |
| `additional[]` entry with `tag: ton_v3` on a TON chain, its `rpc.url` | `rest-indexer`, on a second upstream (see "TON split" below) |
| `bitcoin.esplora.url` | `rest-additional`, on the same upstream (see "Bitcoin esplora" below) |

The type of the `rpc.url` connector comes from `chains.GetChain(chain).Type`, following the
rule dshackle applies internally:

| nodecore chain type | connector type |
|---|---|
| `eth-beacon-chain`, `avm`, `ton` | `rest` |
| `cosmos` | `tendermint` |
| anything else | `json-rpc` |

URLs are copied unchanged. This is correct for TON too: both systems expect the `/api/v2`
prefix inside the v2 base URL and no prefix on the v3 one.

**TON split.** dshackle describes a TON node and its v3 indexer as one upstream: the main
`rpc.url` is the v2 API and an `additional[]` entry tagged `ton_v3` is the indexer. nodecore
recommends two upstreams of the same chain, one with a single `rest` connector and one with a
single `rest-indexer` connector, so that each API gets its own head, health, chain validation
and lower bounds. The converter produces that layout:

| dshackle setting | v2 upstream | v3 upstream |
|---|---|---|
| `id` | the original id | `<id>-v3` |
| `chain`, `labels`, `options`, `custom-headers` | copied | copied |
| main `rpc` block (URL, auth, `tls.ca`) | `rest` connector | not used |
| the `ton_v3` entry's `rpc` block (URL, auth, `tls.ca`) | not used | `rest-indexer` connector |
| `methods.disabled` | copied | copied |
| `methods.enabled` | copied | not copied |
| manual lower bounds | copied | not copied |

`methods.enabled` stays on the v2 upstream only: nodecore does not check method names against
the spec, so a v2 method enabled on the indexer upstream would be routed to the indexer.
Manual lower bounds describe the node's data window; the indexer's window is independent and
nodecore detects it.

The report gets one line under "needs manual action" naming both ids, because `<id>-v3` is a
new upstream id for anything that addresses upstreams by id. The two halves are validated
separately (5.8), so one can be left out while the other is kept. If `<id>-v3` already exists
in the input, the whole-file validation fails on the duplicate id.

A TON upstream without a `ton_v3` entry is converted like any other: one upstream with a
`rest` connector.

**Bitcoin esplora.** dshackle answers `listunspent` from the esplora endpoint of a bitcoin
connection. nodecore does the same through a `rest-additional` connector: the `bitcoin` method
spec bundles the esplora methods and translates `listunspent` into esplora's
`GET /address/{addr}/utxo`. The endpoint therefore becomes a second connector of the same
upstream:

```yaml
# dshackle                            # nodecore
connection:                           connectors:
  bitcoin:                              - type: json-rpc
    rpc:                                  url: http://node:8332
      url: http://node:8332             - type: rest-additional
    esplora:                              url: http://esplora:3000
      url: http://esplora:3000
```

| Case | Result |
|---|---|
| `esplora.basic-auth`, `esplora.bearer-auth`, `esplora.tls.ca` | converted by the `rpc` rules of the table below |
| The upstream's `custom-headers` | not added to the esplora connector; dshackle sends them to the node only |
| `esplora` without a `url` | no connector, as dshackle ignores such a block; its keys are reported by the catch-all |
| The chain's method spec has no `rest-additional` methods | no connector, warning; nodecore's validation would otherwise refuse the whole upstream |

An additional connector never drives the head, so the head stays on the `json-rpc` connector.

Other connection fields:

| dshackle | nodecore | Notes |
|---|---|---|
| `rpc.basic-auth`, `ws.basic-auth` | header `Authorization: Basic <base64(username:password)>` on that connector | |
| `rpc.bearer-auth.token`, `ws.bearer-auth.token` | header `Authorization: Bearer <token>` on that connector | when both basic and bearer are set, basic wins (dshackle's behaviour) and a warning is printed |
| `ws.origin` | header `Origin` on the `websocket` connector | only when set. dshackle sends `Origin: http://localhost` when it is unset; the converter does not reproduce that default |
| `rpc.tls.ca` | connector `ca` | |
| `rpc.tls.certificate`, `rpc.tls.key` | none | warning: nodecore does not present a client certificate to the node |
| `rpc.max-connections`, `rpc.queue-size` | none | warning |
| `ws.frameSize`, `ws.msgSize`, `ws.connections` | none | warning |
| `ethereum-pos.upstream-rating` | none | warning |
| `bitcoin.zeromq` | none | warning: nodecore has no ZeroMQ head source and polls the head over `rpc` |
| `additional[]` entries other than `ton_v3` on TON | none | warning |

### 5.5 `connector-mode`

dshackle's effective mode is the configured `connector-mode`, or its default: both URLs set
gives `RPC_REQUESTS_WITH_WS_HEAD`, only `rpc` gives `RPC_ONLY`, only `ws` gives `WS_ONLY`.
`head-connector` is never emitted; under `mode: strict` nodecore then tracks the head over
`websocket` when that connector exists.

| Effective mode | Connectors emitted | Notes |
|---|---|---|
| `RPC_REQUESTS_WITH_WS_HEAD` | `rpc` + `websocket` | matches the default of `strict` |
| `RPC_ONLY`, no `ws.url` | `rpc` | |
| `RPC_ONLY`, `ws.url` set | `rpc` | warning: `ws.url` was unused by dshackle in this mode and is not carried over |
| `WS_ONLY`, no `rpc.url` | `websocket` | |
| `WS_ONLY`, `rpc.url` set | `rpc` + `websocket` | warning: nodecore sends requests over HTTP when both are configured |
| `RPC_REQUESTS_WITH_MIXED_HEAD` | `rpc` + `websocket` | warning: there is no mixed head; the head comes from `websocket` |

### 5.6 Options

Applies to an upstream's `options` and to `cluster.defaults[].options`. Only options present
in the input are emitted; everything else falls back to the defaults of `strict`.

| dshackle option | nodecore |
|---|---|
| `min-peers: 0` | `validate-peers: false`. nodecore reads a zero `min-peers` as "not set" and uses 1, while in dshackle it means "do not check peers". See "Peer options across layers" below |
| `call-limit-size: 0` | none; warning: nodecore reads a zero as "not set" |
| `validate-syncing`, `validate-peers`, `min-peers`, `validate-call-limit`, `call-limit-size`, `disable-validation`, `disable-log-index-validation`, `disable-liveness-subscription-validation` | same name, same value |
| `validation-interval: <n>` (seconds) | `validation-interval` as a duration, printed in Go's form (`45s`; 60 seconds prints as `1m0s`) |
| `validate-chain: <b>` | `disable-chain-validation: <not b>` |
| `disable-upstream-validation` | `disable-settings-validation` |
| `disable-bound-validation` | `disable-lower-bounds-detection` |
| `validate-gas-price` | none; warning: nodecore takes the gas-price rule from the chain registry, with no per-upstream switch |
| `disable-pending-tx-validation`, `balance` | none; warning |
| `timeout` | none; warning (dshackle parses this option but never uses it) |

**Peer options across layers.** dshackle checks peers when `validate-peers` is on (its
default) and `min-peers` is above zero (its default is 1). It merges the two options one by
one across the entries of `cluster.defaults` and the upstream, a later layer winning. nodecore
resolves `validate-peers` and `min-peers` one by one too, so a `validate-peers: false` that
stands for a zero `min-peers` would outlive a later layer that raises `min-peers` again. The
two options are therefore converted as a pair:

| Case | Result |
|---|---|
| Several `cluster.defaults` entries for one chain | The pair is written from the merged dshackle values. `min-peers: 0` followed by `min-peers: 5` gives `min-peers: 5` and no `validate-peers: false` |
| An upstream with a positive `min-peers` over chain defaults with `min-peers: 0` | The upstream also gets `validate-peers: true`, unless the defaults say `validate-peers: false` themselves |
| An upstream with `validate-peers: true` and no `min-peers` over chain defaults with `min-peers: 0` | The upstream gets `validate-peers: false`: dshackle still sees a zero `min-peers` and does not check |
| An upstream that sets neither option | Nothing is written; it inherits the chain defaults |

Only chain defaults that made it into the output count as the layer below an upstream.

### 5.7 `cluster.defaults`

Each entry is `{chains: [...], options: {...}}`. Every listed name is resolved as in 5.4; for
a chain nodecore serves, the converted options go to
`upstream-config.chain-defaults.<canonical name>.options`, and any other name gets a warning.
When a chain appears in several entries (under one spelling or several), the options are
merged in input order and a later value wins. The two peer options are the exception: they
are merged as dshackle values first and written as a pair (5.6).

### 5.8 Validation

Validation uses nodecore's real parser, defaults and validators through
`config.LoadReloadable(doc, doc)`. Called with the same document twice it is exactly
parse + defaults + validate, and it is the only exported entry point that takes bytes.

- **Per upstream.** For each converted upstream the converter builds a document with
  `upstream-config.mode`, `balancing-strategy`, `chain-defaults` and that single upstream.
  If validation fails, the upstream is left out and reported with nodecore's error text. The
  typical case is a connector type the chain's method spec does not declare (for example a
  `ws.url` on a chain without a `websocket` spec).
- **Whole file.** The complete output is validated once more, with `server.grpc-auth`
  removed from the validated copy: its validator requires the key files to exist on disk, and
  they usually do not exist where the conversion runs. This pass catches cross-upstream
  problems (duplicate ids) and server-level ones (port clashes). A failure here ends the run
  with exit code `1` and no file.

What validation does not cover is whether the files behind the copied paths exist where
nodecore will run: the gRPC auth keys (nodecore checks them at startup), the TLS certificate
and key, and a connector's `ca`. An enabled `grpc-auth` section gets a "needs manual action"
line for that reason (5.3).

Both passes run on a copy in which connector URLs holding a `${VAR}` placeholder are replaced
by a fixed valid URL (5.9). The file that is written keeps the placeholders.

### 5.9 `${VAR}` placeholders

dshackle substitutes `${NAME}` (regex `\$\{(\w+?)}`) in every string value from a JVM
property or an environment variable. nodecore does not. A carried-over value that contains a
placeholder is written unchanged and reported with the upstream id and field, so the operator
can replace it.

**Placeholders in URLs.** A placeholder in the path or query (`...?dkey=${DRPC_KEY}`) still
parses as a URL. One in the host or port, or one that is the whole URL
(`http://${HOST}:8545`, `${NODE_URL}`), does not: nodecore requires a scheme and a host. The
upstream is kept in both cases and written with the placeholder as is. For validation only
(5.8), every connector URL that contains a placeholder is replaced by a fixed valid URL in the
validated copy, so the rest of the upstream is still checked. The warning for such a URL says
that nodecore will not start until the placeholder is replaced.

**Placeholders in `basic-auth`.** When a username or password contains a placeholder, the
`Authorization` header is **not** emitted (encoding would hide the literal placeholder inside
base64) and the warning says the header must be added by hand.

### 5.10 Report

Printed to stderr after conversion. Warnings of the same kind are grouped into one line with
the ids of the upstreams they apply to, so a setting present on every upstream (such as
`node-id`) does not produce one line per upstream. Lines keep input order.

The first line counts entries of `cluster.upstreams`. An entry counts as converted when at
least one nodecore upstream came out of it, so a split TON upstream counts once.

```
converted 12 of 14 upstreams -> /path/nodecore.yml

skipped upstreams:
  xrp-1: chain 'ripple' is not supported by nodecore
  remote-eu: dshackle-to-dshackle upstreams (connection.grpc) are not supported

needs manual action:
  eth-1 connection.generic.rpc.url: contains ${DRPC_KEY}; nodecore does not substitute
    environment variables, replace it with the value
  tls.client: nodecore does not verify client certificates; the setting was not carried over

not carried over:
  cluster.include: only this file is converted; upstreams-extra.yaml was not read
  node-id (eth-1, eth-2, bsc-1): not used by nodecore
  health.blockchains: per-chain availability requirements are not supported; use /ready
  proxy: nodecore has no route-id HTTP proxy
```

### 5.11 Output shape

The converter fills nodecore's own config structs and marshals them (decision 13). The yaml
tags of the structs it writes carry `omitempty`, so a field the converter did not set is not
printed: `AppConfig`, `ServerConfig`, `TlsConfig`, `GrpcAuthConfig`, `UpstreamConfig`,
`Upstream`, `ChainDefaults`, `ApiConnectorConfig`, `MethodsConfig` and `chains.Options`. Two
tags are left as they were, `enabled` in `TlsConfig` and in `GrpcAuthConfig`, so that a
disabled section still says `enabled: false` (5.3).

Key order follows the field order of those structs. Durations print in Go's form.

```yaml
server:
  grpc-port: 2449
  metrics-port: 8081
  health-port: 8082
  tls:
    enabled: true
    certificate: /path/cert.crt
    key: /path/key.p8.key
  grpc-auth:
    enabled: true
    public-key-owner: drpc
    provider-private-key-path: /path/private.pem
    external-public-key-path: /path/public.pem
upstream-config:
  upstreams:
    - id: eth-1
      chain: ethereum
      connectors:
        - type: json-rpc
          url: http://localhost:8545
          headers:
            Authorization: Basic OWMxOTlhZDg6MjU4ZmU0MTQ=
        - type: websocket
          url: ws://localhost:8546
          headers:
            Origin: http://localhost
      methods:
        enable:
          - parity_trace
        disable:
          - admin_shutdown
      options:
        disable-chain-validation: true
      labels:
        archive: "true"
  chain-defaults:
    ethereum:
      options:
        min-peers: 10
  balancing-strategy: base
  mode: strict
```

Sections with nothing to say are omitted. Upstreams keep input order.

A field added to one of these structs later without `omitempty` would be printed with its zero
value in every converted file. The end-to-end fixture test compares the output exactly, so it
fails when that happens.

## 6. Edge cases

### 6.1 Silent by agreement

Only two things are dropped without a warning: `chain-settings` (decision 4) and `version`.
`role: primary` / `standard` is also silent because the behaviour is unchanged.

### 6.2 Others

- **`Authorization` in `custom-headers` together with `basic-auth` or `bearer-auth`.** The
  auth section wins and a warning names the upstream. (My call; dshackle's precedence here was
  not established.)
- **A method in both `methods.enabled` and `methods.disabled`.** nodecore rejects this; the
  upstream is dropped by per-upstream validation with that error.
- **`cluster.defaults` naming a chain with no upstream.** Emitted anyway; harmless.
- **Fixed lower bound with a value below 1.** dshackle ignores it; the converter skips it with
  a warning (nodecore requires `>= 1`).
- **Unknown bound type name.** Warning, not emitted.
- **A key the converter has no rule for** (a typo, or an option added to dshackle later).
  Reported by the catch-all with its full path.
- **Port clash** (for example dshackle's gRPC port equal to nodecore's default HTTP port
  `9090`). Caught by the whole-file validation.
- **Empty or missing `cluster.upstreams`.** Exit code `1`.

## 7. Implementation notes

Left to the builder: the file split inside `cmd/dshackle-convert/` (package `main`), the
input reading technique (the only requirement is that unread keys can be listed), and the
exact warning wording.

Constraints:

- `gopkg.in/yaml.v3`, already a dependency.
- Under `internal/` and `pkg/` only the yaml tags named in 5.11 change; no code.
- Repo conventions apply: exported before unexported, `new(x)` for pointers, no nil-receiver
  guards, no locals shadowing package-level identifiers.

## 8. Testing

Fixtures live in `cmd/dshackle-convert/testdata/`: an input file, the expected output file
and the expected report for each case. No real-world config is available, so inputs are
built from dshackle's documentation examples and its config readers.

Cases:

1. Full production-style config: port, TLS, auth, monitoring, health, several EVM upstreams
   with `rpc` + `ws`, labels, methods, options, defaults.
2. One upstream per connector rule: beacon chain (`rest`), Algorand (`rest`), TON with a
   `ton_v3` endpoint (split into a `rest` upstream and a `rest-indexer` upstream), Cosmos (`tendermint`), Solana
   (`json-rpc`) and Bitcoin (`json-rpc`, plus `rest-additional` for an esplora endpoint: with
   auth and CA, without a url, on a chain without esplora methods, and one case through
   nodecore validation).
3. Every `connector-mode` row of 5.5.
4. Auth headers: basic, bearer, both, `ws.origin`, `custom-headers`, and the `Authorization`
   conflict.
5. Every option row of 5.6, at upstream level and through `cluster.defaults` (including a
   chain listed twice).
6. Every skip reason of 5.4, including a validation rejection.
   Method groups: carried over on an EVM chain, a group the chain lacks, a group in both
   lists, a disabled method inside an enabled group, and one case through nodecore validation.
7. Every warning-only setting of 5.3 and 5.4.
8. `${VAR}` in a URL's query, in its host, as the whole URL, in a bearer token and in
   `basic-auth`; the upstream is kept in every case.
9. Catch-all: an unknown key at top level and inside an upstream.
10. Failures: unparsable input, no convertible upstream, existing `-out` without `-force`.

Decision 13 is guarded by two tests: one marshals a minimal config and compares the text
exactly, so a field without `omitempty` shows up as an unexpected key; another checks that
values the converter does set survive, including `enabled: false` of a disabled section.

Tests need the generated chain registry, like the rest of the repo's tests.

## 9. Follow-up: migration guide (separate deliverable)

A document kept outside this repo. Agreed outline:

1. What stays the same (gRPC protocol, port, TLS certificate, auth keys).
2. Converting the config with this tool.
3. Fixing by hand: one entry per warning type.
4. Launching nodecore (Docker image `drpcorg/nodecore`, config path, mounts, ports,
   `LOG_FORMAT` / `LOG_LEVEL`).
5. Verifying (`/ready`, `/status`, metrics, a healthy startup log).
6. What is different (metric names, health endpoints, no HTTP proxy routes, config reload).

Cut-over and rollback are deliberately not covered.

## 10. Open questions

None. The calls marked "mine" in section 4 and "my call" in 5.5 and 6.2 are the ones to check
during review.
