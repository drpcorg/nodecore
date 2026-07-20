# Ripple (XRP Ledger) family support — design

Date: 2026-07-17
Status: approved (pre-implementation)

## Goal

Add first-class support for the **Ripple / XRP Ledger** blockchain family to
nodecore (`BlockchainType = "ripple"`), covering ripple mainnet and testnet,
with full gRPC (emerald) API support, so these chains can be served by
nodecore instead of dshackle.

**Not to be confused with the `xrpl` chain** in `chains.yaml` — that is the
XRPL **EVM sidechain** (`type: eth`, chain-id 0x15f900), already served by the
EVM family. This design is about native rippled.

## Scope

- **In scope:** rippled JSON-RPC-over-HTTP upstreams (plain HTTP, no auth);
  the dshackle method surface minus `subscribe`/`unsubscribe` (see method
  table); validated-head tracking; network-id chain validation; server-state
  health; rippled/clio client labels; `complete_ledgers`-based lower bounds
  **that may legally decrease** (archival backfill); response normalization
  for XRPL's error-inside-`result` convention (routing-side only, wire shape
  preserved).
- **Out of scope (YAGNI):** WebSocket support — XRPL WS uses a distinct
  `{"command", "id"}` framing; nodecore's `WsProtocol` interface is pluggable
  but an XRPL protocol is real work, and v1 poll head + HTTP serving needs no
  WS (follow-up). `subscribe`/`unsubscribe` methods: over public HTTP they
  are an admin-only callback mode that always errors — exposing them as
  callable methods is a footgun; they return nodecore's method-not-found and
  come back only with WS support. Clio deployment concerns (we run plain
  rippled only; clio-only methods stay passthrough and error on the node).
  Cache tag-parsers (all `cacheable: false` in v1). Range-aware routing by
  per-node history depth (v1 has one node per network).

## Approach

Ripple is a JSON-RPC blockchain family with a poll-only head and a
data-driven method spec, with **two XRPL-specific twists**:

1. **Non-standard envelope.** rippled HTTP API is `{"method", "params":
   [{one object}]}` — no `jsonrpc`, no `id`; responses are `{"result": {...,
   "status": "success"}}` with neither `jsonrpc` nor `id` echoed. nodecore's
   standard 2.0 request envelope is tolerated (extra fields ignored) and
   nodecore re-attaches the client id itself, so **no request-side changes
   are needed**. Internal requests must pass `[{}]`-style params (an empty
   `[]` is rejected with a plain-text HTTP 400 `params unparsable`).
2. **Errors live inside `result`.** Application errors are HTTP 200 with
   `{"result": {"error": "code", "error_code": N, "error_message": "...",
   "status": "error", "request": {...}}}` — no top-level `error` key, so both
   nodecore's parser *and dshackle's* classify them as success. Clients
   therefore expect the native XRPL error shape on the wire (xrpl.js parses
   it), and we must keep it — see Response normalization.

### Verified live API shapes (from our production nodes, rippled 3.2.0)

`server_state [{}]` → `result.state`: mainnet `{"server_state":"full",
"network_id":<ABSENT>,"complete_ledgers":"105660494-105662737",
"build_version":"3.2.0","peers":23,"validated_ledger":{"seq":105662737,
"hash":"...",...},...}`; testnet has `"network_id":1` and
`"complete_ledgers":"32570-19145030"` (near-full history).

**`network_id` is absent on mainnet** (mainnet = 0, the field is only emitted
when configured; absent ⇒ 0). dshackle's validator deserializes it as a
non-nullable int — worth hardening in ours.

`ledger_closed [{}]` → `{"ledger_hash":"...","ledger_index":105662741,
"status":"success"}`.

`ledger [{"ledger_index":"validated"}]` → `{"ledger":{...,"parent_hash":"...",
"close_time":...},"ledger_hash":"...","ledger_index":105662741,
"validated":true,"status":"success"}` — includes the parent hash.

Errors (all HTTP 200): `{"result":{"error":"unknownCmd","error_code":32,
"error_message":"Unknown method.","request":{"command":"foobarmethod"},
"status":"error"}}`; same shape for `lgrNotFound` (21), `txnNotFound` (29),
`invalidParams` (31). Transport-level failures are **HTTP 400/403/503 with
plain-text bodies** (`params unparsable`, `Null method`, ...) — not JSON.

### History quirk: the lower bound moves BOTH ways

Non-archival rippled keeps ~2000 recent ledgers (online deletion; our mainnet
node holds ~2h of history), so the low edge of `complete_ledgers` normally
climbs. But when archival is enabled (`online_delete=0` + `ledger_history
full`) the node **backfills history backwards** and the low edge *decreases*
over time toward genesis (32570). `complete_ledgers` can also be disjoint
(`"a-b,c-d"`) or the literal `"empty"`.

nodecore's shared `BaseLowerBoundProcessor` currently drops any bound lower
than the last published one (`processBounds`: `data.Bound >= bound ||
data.Bound == 1`) — a backfilling ripple archive would be stuck at its first
observed bound until it hits exactly 1. The underlying `LowerBounds.
UpdateBound` already handles decreases (`resetBound`), so the fix is scoped
to the processor filter: detectors that implement an optional
`AllowsBoundDecrease() bool` capability interface bypass the monotonic
filter. Only the ripple detector opts in; every other family keeps today's
behavior. Unit test: bound decreasing between ticks must be published.

## Architecture

### Registry and factory

- `pkg/chains/chains.go`: add `Ripple BlockchainType = "ripple"`, accept it in
  `IsValidBlockchainType`, add `case Ripple: return "ripple"` to
  `getMethodSpecName`.
- `upstream_factory.go`: `case chains.Ripple` →
  `ripple_specific.NewRippleChainSpecificObject(...)`.
- `chains.yaml` already registers ripple (mainnet chain-id `0` grpcId 1100,
  testnet chain-id `1` grpcId 10128, `fork-choice: quorum`, 4s block time,
  lags 10/5) and the gRPC ChainRefs exist — no registry data work.

### Head tracking (`ripple_specific`)

Poll-based `RpcHead`. **Deviation from dshackle, deliberate:** dshackle polls
`ledger_closed`, which returns the most recently *closed* ledger — "not
necessarily validated and immutable yet". We poll
`ledger [{"ledger_index":"validated"}]` instead: the validated head is what
consensus confirmed, matches the WS `ledger` stream semantics (events fire on
validation), and gives us `parent_hash` for free. `GetFinalizedBlock` =
`GetLatestBlock` (the validated head *is* finality on XRPL).
`BlockProcessor()` nil; WS subscription methods return "unsupported".

### Response normalization (the key piece)

A spec-level translator (extending the existing flow-layer
`(spec, method) → translator` registry with a per-spec **wildcard** lookup,
since the error shape is uniform across all rippled methods) inspects
successful-looking responses for `result.status == "error"`:

- **Node-health errors → converted to real retryable errors** so the flow
  retries another upstream: `noNetwork`, `noCurrent`, `noClosed`, `tooBusy`,
  `amendmentBlocked`, `notReady`, `failedToForward`. The client only ever
  sees the converted JSON-RPC error if *all* upstreams fail — acceptable
  deviation.
- **Deterministic request errors → passed through untouched** (`unknownCmd`,
  `invalidParams`, `lgrNotFound`, `txnNotFound`, `actNotFound`, ...): exact
  dshackle wire parity — the client receives the native XRPL error shape with
  HTTP 200, as xrpl.js expects. No retry (correct: same answer everywhere;
  `lgrNotFound` is per-node, but v1 has one node per network — range-aware
  retry is a follow-up together with multi-node pools).

`TranslateRequest` is a no-op (envelope pass-through).

### Chain validation

`RippleChainValidator`: `server_state [{}]` → `state.network_id`, **absent
field defaults to 0** (mainnet). Compare against the configured chain-id
(`0` mainnet / `1` testnet). Mismatch → FatalSettingError; fetch/parse error →
SettingsError.

### Health validation

`RippleHealthValidator` uses `server_state`. **Mapping fixed relative to
dshackle** (which marks `syncing` as UNAVAILABLE and ignores `validating`):

- `full` / `validating` / `proposing` / `tracking` → Available (`tracking` =
  "in agreement with the network", serving-ready for validated queries);
- `connected` / `syncing` → Syncing;
- `disconnected` or anything else → Unavailable;
- `amendment_blocked: true` (field present only when blocked) → Unavailable —
  the node is permanently stuck until a binary upgrade;
- peers (when `validate-peers`): `state.peers == 0` → Unavailable.

### Labels and lower bounds

- Labels from `server_info`: `clio_version` present → client `clio`, else
  `build_version` → client `rippled` (dshackle parity; we run rippled only
  but clio detection is free).
- `RippleLowerBoundDetector`: parse `state.complete_ledgers` — `"empty"` →
  fallback (cached/unknown); single or disjoint ranges → the **start of the
  last range** (the contiguous window ending at the tip) as STATE and BLOCK
  bounds. Implements `AllowsBoundDecrease() = true` (backfill). Period
  2 min — a non-archival window slides ~1 ledger per 4s and archival
  backfill moves fast. dshackle hardcodes STATE=1 here, which is simply
  wrong for a 2000-ledger node; this is a fix, not parity.

### Method spec (`pkg/methods/specs/ripple.json`)

dshackle's surface minus `subscribe`/`unsubscribe` (36 methods), all
`cacheable: false` in v1:

| group | methods |
|---|---|
| accounts | account_channels, account_currencies, account_info, account_lines, account_nfts, account_objects, account_offers, account_tx, gateway_balances, noripple_check |
| ledger | ledger, ledger_closed, ledger_current, ledger_data, ledger_entry, ledger_index |
| tx | tx, tx_history, transaction_entry |
| dex/paths | book_offers, deposit_authorized, path_find, ripple_path_find |
| nft | nft_buy_offers, nft_sell_offers, nft_history, nft_info, nfts_by_issuer |
| channels | channel_authorize, channel_verify |
| server | fee, manifest, server_info, server_state, ping, random |
| broadcast | submit, submit_multisigned |

`submit`/`submit_multisigned` dispatch as broadcast — safe on XRPL (a signed
tx is pinned by hash and account sequence; identical resubmission cannot
apply twice). Clio-only methods (`ledger_index`, `nft_history`, `nft_info`,
`nfts_by_issuer`) are kept for dshackle parity and pass the node's
`unknownCmd` through on our rippled-only pool.

## Testing

1. **Unit tests** (family parity): validated-ledger head parsing (incl.
   parent hash), network-id validation with the absent-field-means-mainnet
   case, all server_state health mappings incl. `amendment_blocked`,
   rippled/clio label parsing, `complete_ledgers` parsing ("empty" / single /
   disjoint) **and the decreasing-bound publication test** against the
   processor, error-normalization split (retryable converted, deterministic
   passed through byte-exact), method-spec loading.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   runs locally against our production mainnet + testnet rippled nodes over
   VPN. Corpus: every spec method node-direct vs nodecore — ledgers by
   validated/index/hash, a ledger below the mainnet window (`lgrNotFound`
   passthrough byte-exact), account methods on known accounts, tx/txnNotFound,
   fee/server_info/server_state shapes, unknown method (`unknownCmd`
   passthrough — note: nodecore's own -32601 applies only to methods absent
   from the spec), invalid params, clio-only methods on rippled
   (`unknownCmd` passthrough). `submit` only with a deterministically invalid
   blob on both networks; nothing broadcast.
   **Executed 2026-07-17 — passed.** 46/46 checks identical across mainnet
   and testnet; every deterministic case byte-exact on the full `result`
   object, including error passthroughs (`lgrNotFound`, `txnNotFound`,
   `invalidParams`, `unknownCmd`, invalid `submit`) and the `ledger_data`
   pagination marker. Live findings: `path_find` over HTTP errors as
   `noEvents` (passed through byte-exact); `channel_verify` with a bad
   signature is a *success* with `signature_verified:false`, not an error;
   `network_id` absent on mainnet on both paths. Lower bounds published live
   from `complete_ledgers`. Methods absent from the spec (e.g. `version`)
   get nodecore's own -32601 — standard for all families.

3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- XRPL WebSocket protocol (`{"command"}` framing) for subscriptions and a
  subscription-backed head.
- Cache tag-parsers keyed on explicit validated `ledger_index`/`ledger_hash`.
- Range-aware routing across mixed-history pools (needs per-upstream
  `complete_ledgers` in routing state).
- starknet, ton follow as separate family designs.
