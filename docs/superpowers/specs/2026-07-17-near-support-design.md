# NEAR family support — design

Date: 2026-07-17
Status: approved (pre-implementation)

## Goal

Add first-class support for the **NEAR** blockchain family to nodecore
(`BlockchainType = "near"`), covering near mainnet and testnet (betanet is
registered in `chains.yaml` and gets family support for free), with full gRPC
(emerald) API support, so these chains can be served by nodecore instead of
dshackle.

## Scope

- **In scope:** neard JSON-RPC 2.0 upstreams (plain HTTP, no auth, no WS —
  nearcore has no subscriptions at all); the 14-method surface currently
  served by dshackle (see method table); optimistic head tracking; chain-id
  validation; syncing/peers health; client version labels; dynamic GC-window
  lower bounds.
- **Out of scope (YAGNI):** stable aliases nearcore added later
  (`changes`, `genesis_config`, `block_effects`, `maintenance_windows`,
  `light_client_proof`) and `broadcast_tx_async`/`broadcast_tx_commit`/
  `health`/`client_config`/light-client methods — none are served by dshackle
  today, so no client depends on them through us; per-method cache tag
  parsers (v1 ships everything `cacheable: false`, parity with dshackle which
  does not cache NEAR); a separately tracked finalized head (see Head
  tracking); archival-aware routing (we run no archival NEAR nodes).

## Approach

NEAR follows the **bitcoin family template** (JSON-RPC, poll-only head,
data-driven method spec): a `near_specific` chain-specific package plus
`pkg/methods/specs/near.json`. It is strictly simpler than bitcoin — a single
`status` call powers health, chain validation, labels and lower bounds; there
is no esplora-style secondary connector and no method aliases to translate.

### Verified live API shapes (from our production nodes, nearcore 2.13.1)

`status` (truncated): `{"chain_id":"mainnet","protocol_version":84,
"latest_protocol_version":86,"version":{"version":"2.13.1","build":"unknown",
"rustc_version":"1.93.0"},"validator_account_id":null,"sync_info":{
"latest_block_height":207365026,"latest_block_hash":"...",
"latest_block_time":"2026-07-17T...","earliest_block_height":207157933,
"syncing":false},...}` — testnet identical shape with `"chain_id":"testnet"`.

`block {"finality":"optimistic"}` → `{"author":...,"header":{"height":...,
"hash":"<base58>","prev_hash":"<base58>","timestamp":1784299563227484539,
"timestamp_nanosec":"1784299563227484539",...},"chunks":[...]}`. The
optimistic head runs ~3 blocks ahead of `"final"`. `timestamp` is integer
nanoseconds (int64-boundary risk in some parsers; `timestamp_nanosec` is the
string twin).

`gas_price [null]` → `{"gas_price":"100000000"}` (string).

Errors carry a structured `name`/`cause` pair alongside the standard fields:

```json
{"error":{"name":"HANDLER_ERROR","cause":{"name":"UNKNOWN_BLOCK","info":{}},
 "code":-32000,"message":"Server error","data":"DB Not Found Error: ..."}}
```

- Unknown method → `REQUEST_VALIDATION_ERROR`/`METHOD_NOT_FOUND`, `-32601`.
- Garbage-collected block (`block`/`chunk`) → `UNKNOWN_BLOCK` (`query`
  returns the more explicit `GARBAGE_COLLECTED_BLOCK`).
- **Unknown transaction**: the node holds the request ~10s (server-side
  poll loop, 10s default timeout) and returns HTTP **408** with
  `cause.name: "TIMEOUT_ERROR"` — this is not a node-health signal and must
  not be blindly retried in a tight loop.
- Syncing node → `NOT_SYNCED_YET` / `NO_SYNCED_BLOCKS` (retryable on another
  upstream); user errors (`INVALID_TRANSACTION`, `PARSE_ERROR`, ...) are
  deterministic.

## Architecture

### Factory and type plumbing

- `upstream_factory.go`: new `case chains.Near` returning
  `near_specific.NewNearChainSpecificObject(...)`.
- `chains.go`: `getMethodSpecName` gains `case Near: return "near"`
  (currently resolves to `""`). `chains.Near` and the NEAR/NEAR_TESTNET/
  NEAR_BETANET constants, `chains.yaml` entries (`chain-id: mainnet|testnet|
  betanet`, lags 40/20, 1s block time) and gRPC ChainRefs (1050/10064/10065)
  all already exist — no registry work.

### Head tracking (`near_specific`)

Poll-based `RpcHead` (no subscriptions), dshackle parity:

1. `GetLatestBlock`: `block {"finality":"optimistic"}` → height,
   hash/prev_hash (base58 strings fed to `NewHashIdFromString` as-is).
2. `GetFinalizedBlock`: `block {"finality":"final"}` — implemented for
   correctness, but `BlockProcessor()` returns nil in v1, so no separate
   finalized-head poll loop runs (same simplification as every non-EVM
   family; the optimistic/final gap is ~3 blocks / ~3s). Revisit only if
   consumers need a distinct final head.

### Chain validation

`NearChainValidator` calls `status` and compares `chain_id` (case-insensitive)
against the configured `chain-id` (`mainnet`/`testnet`/`betanet` — NEAR
chain-ids in `chains.yaml` are network-name strings, not hex). Mismatch →
`FatalSettingError`; fetch error → `SettingsError`. This is exactly
dshackle's check; no genesis call needed (chain_id is unique per network,
unlike bitcoin where two forks both report `main`).

### Health validation

`NearHealthValidator` uses the same `status` payload:
- `sync_info.syncing == true` → Syncing;
- stale head guard: `latest_block_time` older than the chain's syncing-lag
  worth of block time → Syncing (catches the frozen-head case `syncing:false`
  does not);
- peers (when `validate-peers`): `network_info` → `num_active_peers == 0` →
  Unavailable.

### Labels and lower bounds

- `client_version` label from `status.version.version` (e.g. `2.13.1`),
  client type `neard` (the only production implementation).
- `NearLowerBoundDetector`: `status.sync_info.earliest_block_height` →
  emitted as STATE and BLOCK bounds (dshackle parity). Our nodes are all
  non-archival with a sliding ~5-epoch (~2.5 day) GC window, so the bound is
  re-polled on a short period (3 min); no binary search needed — the node
  reports its own boundary.

### Method spec (`pkg/methods/specs/near.json`)

The 14 methods dshackle serves, all `cacheable: false` in v1:

| group | methods | notes |
|---|---|---|
| default | block, chunk, tx, EXPERIMENTAL_tx_status, EXPERIMENTAL_receipt, query, EXPERIMENTAL_changes, EXPERIMENTAL_changes_in_block, EXPERIMENTAL_protocol_config, gas_price, validators | object params (`{"finality":...}` / `{"block_id":...}`) |
| passthrough | status, network_info | node-local, never cache |
| broadcast | send_tx | broadcast to all upstreams (dshackle used BroadcastQuorum) |

No translations, no bans: every upstream is a full neard and serves the whole
surface. Follow-up (not v1): cache tag-parsers for `block`/`chunk`/`tx` by
hash and `query` with explicit `block_id` via the existing jq `tag-parser`
mechanism.

## Testing

1. **Unit tests** (bitcoin-family parity): optimistic/final head parsing
   (incl. the nanosecond timestamp), chain-id validation mainnet-vs-testnet
   cross-check, syncing/stale-head/peers health states, version label,
   earliest-block-height bounds, method-spec loading.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   runs locally against our production near mainnet + testnet nodes (reached
   directly over VPN, plain HTTP, no auth). Corpus: every spec method against
   node-direct vs nodecore — blocks by finality/height/hash, deep GC'd
   heights (UNKNOWN_BLOCK passthrough), chunk by ids, a real tx and an
   unknown tx (408/TIMEOUT_ERROR passthrough within tolerable latency),
   query view_account/call_function, changes, protocol/genesis config,
   gas_price, validators, unknown method, invalid params. `send_tx` only
   with a deterministically invalid transaction on both networks (expected
   error passthrough, nothing broadcast).
3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- Cache tag-parsers and per-method cacheability for finalized data.
- Distinct finalized-head tracking (optimistic vs final) if a consumer needs
  it.
- ripple, starknet, ton follow as separate family designs reusing this
  template.
