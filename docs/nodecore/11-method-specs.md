# Method specs

Method specs define the per-chain RPC method behavior that nodecore enforces at runtime: which methods exist on each chain, which transports they speak (`json-rpc` / `rest` / `grpc` / `websocket`), whether each method is cacheable, whether it must stay on a single upstream ("sticky"), how to extract its block tag for cache-key derivation, and so on.

Specs are **data-driven**: nodecore does not hard-code any of this in Go. To add support for a new RPC method, add or edit a JSON spec file - don't add ad-hoc switches in code.

## Where specs live

JSON files under [`pkg/methods/specs/`](../../pkg/methods/specs/) are embedded into the nodecore binary via `//go:embed`. The complete spec set is shipped with the binary; there is no need to copy or distribute them separately.

Each file declares one *named spec*. The spec name (`spec.name`) is what `chains.yaml` references to attach a spec to a chain.

## Spec file structure

```json
{
  "openrpc": "1.0.0",
  "info": {
    "title": "Human-readable title",
    "version": "1.0.0"
  },
  "spec": {
    "name": "eth-json-rpc",
    "api-connectors": ["json-rpc", "websocket"],
    "type": "plain"
  },
  "spec-imports": [
    "another-spec-name"
  ],
  "methods": [
    {
      "name": "eth_blockNumber",
      "group": "common",
      "settings": { "cacheable": false },
      "tag-parser": { "type": "blockNumber", "path": ".[0]" }
    }
  ]
}
```

### `spec` block

- `name` (string, required) — the spec identifier. Must be unique across all loaded specs.
- `type` (string, required) — either `plain` or `bundle`:
  - `plain` — a spec that contributes its own `methods` and declares its own `api-connectors`.
  - `bundle` — a spec that imports one or more other specs via `spec-imports` and exposes the union. A bundle must not declare `api-connectors` or `methods` of its own.
- `api-connectors` (array of strings) — which transports this spec applies to. Allowed values: `json-rpc`, `rest`, `grpc`, `websocket`, `rest-additional`. **Required on plain specs**, **forbidden on bundle specs**.

  > **⚠️ Order is significant.** nodecore iterates `api-connectors` in the order written and selects the connector for each method in that order. List the transports from preferred to least preferred for the methods in this spec — the first entry is what nodecore will try first when more than one of these connectors is configured on an upstream.

### `spec-imports`

An array of other spec names to merge into this one. Used by bundle specs (e.g. `eth.json` imports `eth-json-rpc` and `eth-websocket` to produce the chain-level `eth` spec).

### `methods` entries

Each method object:

- `name` (string, required) — the RPC method identifier as the client sends it (e.g. `eth_blockNumber`, `getBlock` for Algorand, `POST#/info` for REST endpoints).
- `group` (string) — logical grouping used by routing and per-group toggles. **_Default_**: `common`. Special groups include `filter` (filter-style sticky methods) and `sub` (subscription methods).
- `enabled` (bool) — set to `false` to declare a method but turn it off by default. **_Default_**: `true`.
- `settings` (object) — see below.
- `tag-parser` (object) — see below.

### `settings`

```json
"settings": {
  "cacheable": false,
  "enforce-integrity": false,
  "local": false,
  "dispatch": "broadcast",
  "sticky": { "send-sticky": false, "create-sticky": false },
  "subscription": { "is-subscribe": false, "method": "subscribe", "unsubscribe-method": "unsubscribe" }
}
```

- `cacheable` (bool) — whether responses for this method are eligible for caching. **_Default_**: `true`. Cache policies in [Cache](04-cache.md) only apply to methods marked cacheable.
- `enforce-integrity` (bool) — when `true`, the [integrity](05-upstream-config.md#integrity) check runs for this method (block-number / head consistency). **_Default_**: `false`. Typically set on `eth_blockNumber`, `eth_getBlockByNumber`.
- `local` (bool) — when `true`, the method is synthesized inside nodecore without calling any upstream (e.g. capability discovery methods).
- `sticky` (object) — pins a request to a single upstream:
  - `create-sticky: true` — this method *creates* an upstream-bound resource (e.g. `eth_newFilter` returns a filter ID that only the originating node knows). nodecore records which upstream served the request and prefixes the returned identifier so subsequent calls can be routed back.
  - `send-sticky: true` — this method *consumes* a previously created sticky resource (e.g. `eth_getFilterChanges`). nodecore extracts the upstream identifier from the request payload and routes back to the same upstream.
  - The two flags are mutually exclusive.
- `subscription` (object) — only relevant on WebSocket-style methods:
  - `is-subscribe: true` — declares this method as a subscription open call.
  - `method` (string) — for sub helpers; the underlying JSON-RPC method name when it differs from the entry's `name`.
  - `unsubscribe-method` (string) — the paired unsubscribe method.
- `dispatch` (string) — optional fan-out execution policy for unary methods. Supported values:
  - `broadcast` — nodecore sends the same request to every matching available upstream, waits for fan-out to complete, and returns the first successful response in selected-upstream order (not the fastest response). This is intended for transaction propagation methods such as `eth_sendRawTransaction`. If all upstreams fail, nodecore returns a deterministic upstream/protocol error.
  - `maximum-value` — nodecore sends the request to every matching available upstream and returns the successful response with the largest hex quantity result. This is intended for nonce-like methods such as `eth_getTransactionCount`. Invalid/error responses are ignored if at least one valid value exists.
  - `not-null` — nodecore tries matching upstreams sequentially and returns the first successful non-`null` response. A successful JSON-RPC `null` is treated as a possible indexing lag miss for that upstream, so nodecore tries the next candidate. If all candidates return `null`, the first `null` response is returned. Stream responses are treated as valid non-null responses and returned immediately. This policy is used for lookup methods such as transaction, receipt and block lookups. It is gated by the per-chain `chain-defaults.<chain>.dispatch.not-null` setting: disabled by default in `default` mode and enabled by default in `strict` mode.
  - Dispatch methods must not be `local`, `subscription`, or sticky methods. Fan-out policies (`broadcast`, `maximum-value`) bypass the normal cache processor path because one client request intentionally maps to multiple upstream calls. `not-null` also bypasses the cache path so a cached `null` cannot prevent retrying another upstream.
  - Dispatch increases upstream load and usually makes latency depend on the slowest selected upstream, bounded by existing connector/request timeouts.

### `tag-parser`

Used by the cache subsystem to extract a block tag (or other key component) from the request params. Without a tag parser, methods that take a block tag would all hash to the same cache key.

```json
"tag-parser": {
  "type": "blockNumber",
  "path": ".[1]"
}
```

- `path` (string, required) — a [gojq](https://github.com/itchyny/gojq) query against the request `params` array.
- `type` (string, required) — declares how the extracted value is interpreted:
  - `blockNumber` — a hex block number or a tag (`latest`, `earliest`, `pending`, `finalized`, `safe`).
  - `blockRef` — a block hash, hex number, or tag.
  - `object` — a generic JSON object (the parser returns it as-is for cache-key composition).
  - `string` — a plain string value.
  - `blockRange` — a `{from, to}` range; used for log-style queries.

## REST method routing

For specs with `api-connectors: ["rest"]` or `api-connectors: ["rest-additional"]`, method names follow the convention `VERB#/path/template`. Wildcards in the template (`*`) capture path segments. At request time, the HTTP server matches the incoming `METHOD /path` against the registered templates - see [`MatchRestMethod`](../../pkg/methods/helpers.go) - and the captured segments are forwarded to the upstream as `PathParams`.

Example (Hyperliquid):

```json
{
  "spec": {
    "name": "hyperliquid-rest-additional",
    "api-connectors": ["rest-additional"],
    "type": "plain"
  },
  "methods": [
    { "name": "POST#/info",     "settings": { "cacheable": false } },
    { "name": "POST#/exchange", "settings": { "cacheable": false } }
  ]
}
```

`rest-additional` is reserved for specs that augment an upstream whose primary transport is something else. An upstream cannot consist of only `rest-additional` connectors (see [Upstream config](05-upstream-config.md#connectors)).

## Bundle example

A bundle stitches together transport-specific plain specs:

```json
{
  "openrpc": "1.0.0",
  "info": { "title": "Ethereum JSON-RPC and websocket methods", "version": "1.0.0" },
  "spec": {
    "name": "eth",
    "type": "bundle"
  },
  "spec-imports": [
    "eth-json-rpc",
    "eth-websocket"
  ]
}
```

The resulting `eth` spec carries every method declared by `eth-json-rpc` plus every method declared by `eth-websocket`, attached to the corresponding `api-connectors`.

The `tron` bundle is the multi-transport example: it composes `tron-json-rpc` (Ethereum-compatible `/jsonrpc`), `tron-rest` (the canonical `/wallet/*` HTTP API), and `tron-rest-solidity` (a `rest-additional` mirror over `/walletsolidity/*` for confirmed-only reads). The resulting `tron` spec carries methods across all three connectors at once.

## Shipped specs

As of today, nodecore embeds the following spec files (see `pkg/methods/specs/`):

```
algorand        eth-json-rpc   harmony_1                       linea         scroll
arbitrum        eth-websocket  hyperliquid                     mantle        sei
aztec           eth            hyperliquid-rest-additional     optimism      solana
cronos_zkevm    fantom         klaytn                          polygon       solana-json-rpc
filecoin        harmony_0      klaytn-json-rpc                 polygon_zkevm solana-websocket
                                klaytn-websocket                rootstock     tron
                                                                              viction
                                                                              zk
```

## Adding a new method

1. Find or create the relevant plain spec under `pkg/methods/specs/` (one per transport).
2. Append a `methods[]` entry. The minimum is `{ "name": "<method_name>" }`; everything else defaults sensibly (`cacheable: true`, `group: "common"`).
3. If the method takes a block tag and should be cache-aware, add a `tag-parser`.
4. If the method opens or consumes a server-side resource (filters, subscriptions), set the appropriate `sticky` or `subscription` flags.
5. Rebuild the binary. There is no Go change required.

> The embedded spec set is the source of truth - nodecore loads it at startup via `specs.NewMethodSpecLoader().Load()` in `cmd/nodecore/main.go`. The codebase exposes a constructor (`NewMethodSpecLoaderWithFs`) for tests to inject a custom filesystem, but there is no production-side environment-variable override today.
