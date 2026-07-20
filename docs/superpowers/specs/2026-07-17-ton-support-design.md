# TON family support — design

Date: 2026-07-17
Status: approved (pre-implementation)

## Goal

Add first-class support for the **TON** blockchain family to nodecore
(`BlockchainType = "ton"`), covering ton mainnet (testnet is registered in
`chains.yaml` and gets family support for free), with full gRPC (emerald)
API support, so the chain can be served by nodecore instead of dshackle.

## Scope

- **In scope:** toncenter-style REST upstreams with **two self-contained
  APIs**: the v2 HTTP API (`rest` connector, base URL carries the `/api/v2`
  prefix) and the v3 indexer (`rest-additional` connector). Unlike
  dshackle's `additional_endpoints` kludge, v3 is a first-class citizen with
  **two deployment modes** (see Deployment modes): split — a standalone v3
  upstream with fully independent accounting (head, health, chain
  validation, labels, lower bounds via the v3 API) — and combined — both
  connectors on one upstream, where accounting is v2-only and the v3
  connector serves methods only, with a warning logged. The dshackle method
  surface (v2 REST + `POST /jsonRPC` wrapper + `/api/v3/*`); race-free chain
  validation via zerostate hashes (v2) / `global_id` (v3).
- **Out of scope (YAGNI):** WebSocket (none exists in either API); cache
  tag-parsers (`cacheable: false` v1); v3-vs-v2 head divergence detection
  (follow-up); binary-search history-depth detection for non-archival
  liteservers (v1 reports UnknownBound honestly — see Lower bounds);
  response envelope normalization — **none is needed**: v2 errors are
  non-2xx with a structured JSON body that nodecore's REST error parsing
  already understands (`tryParseStructuredError` reads both `message` and
  `error` keys), and the body is preserved for byte-exact client passthrough.

## Approach

TON is a REST family (aptos template) with the bitcoin two-connector twist.
No translations, no envelope surgery.

### Verified live API shapes (from our production mainnet node)

v2 (`ton-http-api-cpp`, everything under `/api/v2`, no auth):

- `GET /api/v2/getMasterchainInfo` → `{"ok":true,"result":{"last":
  {"workchain":-1,"shard":"-9223372036854775808","seqno":80352632,
  "root_hash":"<base64>","file_hash":"<base64>"},"state_root_hash":"...",
  "init":{"workchain":-1,"seqno":0,"root_hash":
  "F6OpKZKqvqeFp6CQmFomXNMfMj2EnaUSOXN+Mh+wVWk=","file_hash":
  "XplPz01CXAps5qeSWUtxcyBfdAo5zVb1N979KLSKD24="}},"@extra":"..."}` — the
  `init` block is the network **zerostate**, a one-call network fingerprint.
- `getBlockHeader(last)` → `"global_id":-239` (mainnet), `gen_utime`, ...
- Errors: HTTP status mirrors the body `code` — 422 validation
  (`{"ok":false,"error":"failed to parse...","code":422,"@extra":"..."}`),
  404 unknown method, 500 `LITE_SERVER_NOTREADY: ... seqno not in db` for
  history misses, 504 liteserver timeout.
- `POST /api/v2/jsonRPC` works but is a thin wrapper returning the same
  toncenter envelope (the C++ implementation does not echo `jsonrpc`/`id`).
- Liteserver history: sliding, roughly days–2 weeks (tip−100k ok, tip−500k
  `seqno not in db`). Not archival.

v3 (toncenter `ton-indexer`, separate port, advertised via the
`additional_endpoints` Consul meta `[{"port":...,"tag":"ton_v3"}]`):

- `GET /api/v3/masterchainInfo` → bare object (no `ok` wrapper):
  `{"last":{...,"global_id":-239,"gen_utime":"..."},"first":{...}}` —
  `first` directly advertises the indexer's history floor. Our indexer
  backfills from tip−100 at first boot (no genesis, ~2 months of data).
- Shape gotcha: `shard` is signed decimal in v2 (`"-9223372036854775808"`)
  but unsigned hex in v3 (`"8000000000000000"`) — passthrough only, nodecore
  does not interpret it.
- Errors: `{"error":"..."}` with 422/404/500; unknown paths are 500
  `Cannot GET ...`. Old-but-missing blocks can be an *empty 200* result
  (`{"blocks":[]}`), not an error.

### TON facts that shape the design

- Masterchain blocks are BFT-final (Catchain, >2/3 signatures) — no reorg
  handling needed; `last.seqno` is a monotonically final head.
- `sendBoc`/`sendBocReturnHash` are effectively idempotent (nodes dedupe by
  message hash; wallet seqno replay protection) — but dshackle serves them
  with AlwaysQuorum (no broadcast) and v1 keeps that parity; broadcast is a
  possible follow-up, noting an "already seen" resend may error on some
  nodes while succeeding on others.
- The tonlib masterchain-ref cache race (validator asks `getBlockHeader` for
  a seqno the API's cache hasn't caught up to → spurious
  `LITE_SERVER_UNKNOWN`) is why dshackle ships
  `disable_upstream_validation` for ton. Our validation avoids the race
  entirely (see Chain validation).

## Architecture

### Deployment modes: v3 is not "additional"

The v2 API's data window is the liteserver's sliding block retention; the
v3 window is whatever range the indexer was backfilled with. They are
independent in both directions — a non-archival liteserver can sit next to a
genesis-deep index and vice versa — and they fail independently (a stalled
indexer must not poison the node's health). nodecore's accounting (head,
health, bounds — all reported to the platform per upstream) cannot express
two windows on one upstream, so:

- **Split mode (recommended):** two upstreams of chain `ton` — a v2
  upstream (single `rest` connector) and a **standalone v3 upstream**
  (single `rest-indexer` connector - a plain type; both APIs already register as
  separate Consul services). Each has full independent accounting:

  | | v2 upstream | v3 upstream |
  |---|---|---|
  | head | `getMasterchainInfo` | `/api/v3/masterchainInfo` |
  | health | v2 liveness | `last.gen_utime` freshness |
  | chain validation | zerostate hashes | `last.global_id` (-239/-3) |
  | labels | `openapi.json` | `doc.json` ("TON Index (Go)") |
  | lower bounds | `lookupBlock seqno=1` probe | `first.seqno` — advertised directly; **may decrease** on deeper backfill (reuses the ripple `DecreasingBoundDetector` opt-in) |

  Method routing between the two upstreams is automatic: each advertises
  only the methods its connector type carries.
- **Combined mode (allowed, warned):** one upstream with both connectors.
  All accounting is computed from the v2 API; the v3 connector serves the
  `/api/v3/*` methods and takes part in **no** validations or calculations.
  A warning is logged at upstream creation stating exactly that.

Shared-machinery changes this requires: a family capability
removed: instead the v3 connector is the new PLAIN type `rest-indexer` - a
self-contained indexer API that is legal standalone by construction, needing no
validation exceptions; it maps to the poll head in
`createHead`. Other families are unaffected — an esplora-only bitcoin
upstream is still rejected at config time.

### Factory and type plumbing

- `chains.go`: `getMethodSpecName` gains `case Ton: return "ton"` (the
  `Ton` BlockchainType already exists and is accepted).
- `upstream_factory.go`: `case chains.Ton` →
  `ton_specific.NewTonChainSpecificObject(...)`.
- `chains.yaml` (mainnet chain-id `"-239"` grpcId 1080, testnet `"-3"`) and
  gRPC ChainRefs already exist. `validate-peers: false` is already set at
  the protocol level — TON's API exposes no peer notion.

### Head tracking (`ton_specific`)

Poll-based `RpcHead`: `GET#/getMasterchainInfo` → height `result.last.seqno`,
hash `result.last.root_hash` (base64 string used verbatim as the block id).
Parent hash: `EmptyHash` — dshackle fills it with a *random string* to dodge
a dedup check; nodecore's near/ripple precedent shows EmptyHash is the
correct way. `GetFinalizedBlock` = `GetLatestBlock` (masterchain head is
final). `BlockProcessor()` nil; subscriptions unsupported.

### Chain validation

`TonChainValidator`: single `getMasterchainInfo` call, compare
`result.init.root_hash`/`file_hash` against the expected **zerostate** per
chain (map keyed by `chains.Chain`, like bitcoin's genesis map; mainnet
values verified live above, testnet values verified against public
endpoints during implementation). Zerostate is unique per network, ships in
the very first response the node can serve, and — unlike dshackle's
`getBlockHeader(global_id)` two-step — cannot hit the tonlib
masterchain-ref race. Unknown chain in the map → warn + Valid (skip);
mismatch → FatalSettingError; fetch error → SettingsError.

### Health validation

dshackle has no ton health validator at all. v1 adds a liveness check:
`getMasterchainInfo` succeeds → Available, fails → Unavailable. Head
staleness (frozen liteserver) is already caught by nodecore's generic
head-lag machinery; a `gen_utime`-freshness probe via `getConsensusBlock`
is a follow-up if the fleet ever needs it.

### Labels

From `GET#/openapi.json` (relative to the v2 base): `info.title` "TON HTTP
API C++" → client `ton-http-api-cpp`, "TON HTTP API" → `ton-http-api`;
`info.version` (e.g. `v2.1.13-9bae630`) as client_version. Fetch error →
no labels (warn), like the starknet two-probe detector's failure path.

### Lower bounds

`TonLowerBoundDetector` probes `GET#/lookupBlock?workchain=-1&shard=
-9223372036854775808&seqno=1` (generous timeout — archival lookups are
slow):
- success → BLOCK/STATE = 1 (archival liteserver);
- `seqno not in db`-class error → **UnknownBound** (honest: a non-archival
  liteserver's window slides and TON exposes no `complete_ledgers`
  equivalent; binary search is the follow-up);
- transport error → cached/UnknownBound fallback (family pattern).
Period 15 min. Our only node today is non-archival → expect UnknownBound.

### Method spec (`pkg/methods/specs/ton.json`)

Bundle of two specs (bitcoin/esplora pattern):

- `ton-http-v2.json`, `api-connectors: ["rest"]` — dshackle's v2 surface:
  the account/block/transaction/config GET methods, `POST#/runGetMethod`,
  `POST#/sendBoc`, `POST#/sendBocReturnHash`, `POST#/sendQuery`,
  `POST#/estimateFee`, and `POST#/jsonRPC`. Method templates are unprefixed
  (`GET#/getMasterchainInfo`); the `/api/v2` prefix lives in the connector
  URL, mirroring dshackle's `rpc_path`.
- `ton-index-v3.json`, `api-connectors: ["rest-additional"]` — dshackle's
  `/api/v3/*` surface (accounts, events/actions, blockchain, jettons, nfts,
  stats, and the api-v2-compat endpoints).

All `cacheable: false` in v1, AlwaysQuorum-equivalent default dispatch
everywhere (dshackle parity — no broadcast, no not-null). Upstreams without
a v3 connector simply never advertise the v3 methods (static
connector-availability filtering, as with esplora).

## Testing

1. **Unit tests** (family parity): masterchain-info head parsing (base64
   hash), zerostate validation incl. mainnet-vs-testnet cross-check and
   unknown-chain skip, liveness health, openapi label parsing (cpp vs
   python title), lookupBlock bound probe (archival / not-in-db / transport
   error), method-spec loading incl. v3 filtered out without
   rest-additional.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   locally against the production mainnet node over VPN (v2 + v3
   connectors). Corpus: every spec method node-direct vs nodecore — v2
   block/account/tx reads at fixed recent blocks, `jsonRPC` wrapper,
   deep-history miss (`LITE_SERVER_NOTREADY` 500 passthrough with status
   parity), 422/404 passthrough, v3 masterchainInfo/blocks/transactions/
   jetton/nft reads, v3 empty-200 semantics, v3 422 passthrough. Send
   methods only with deterministically invalid BoCs (expected error
   passthrough, nothing broadcast). Testnet corpus impossible (no node) —
   zerostate validation for testnet covered by unit tests; rollout note.
   **Executed 2026-07-17 — passed.** 27/27 checks identical against the
   production mainnet node (v2 compared with the volatile `@extra` field
   stripped): block/account/config reads byte-exact, the `jsonRPC` wrapper,
   deep-history `LITE_SERVER_NOTREADY` with **HTTP-status parity** (500/500),
   422 validation on both APIs, invalid `sendBoc` error passthrough, v3
   reads incl. the empty-200 semantics. The `/api/v2` base-path joining and
   v3 `rest-additional` routing verified live; the non-archival liteserver
   correctly reports UnknownBound. The one designed difference: REST paths
   absent from the spec get nodecore's own 400 (`the method GET#/x does not
   exist/is not available`) instead of v2's 404 envelope. Testnet corpus
   impossible (no node); testnet zerostate verified against the public
   endpoint and covered by unit tests — rollout note.

   **Deployment-modes rework validated live 2026-07-20.** Split mode: the
   standalone v3 upstream tracks its own head via `/api/v3/masterchainInfo`
   and publishes its own bounds (STATE/BLOCK = the index floor `first.seqno`)
   while the v2 upstream honestly reports UnknownBound (non-archival
   liteserver) — two independent accounting profiles on one chain, method
   routing splitting automatically. Combined mode: the warning fires at
   upstream creation and all accounting runs via v2 (archival probe →
   UnknownBound), with `/api/v3/*` methods served as before.

3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- Binary-search history-depth detection for non-archival liteservers.
- v3-indexer staleness vs v2 head divergence signal.
- `gen_utime` freshness health probe.
- Broadcast dispatch for sendBoc (idempotent, but needs "already seen"
  reconciliation).
- celestia/stellar need `chains.yaml` registry entries first; they are the
  last dshackle-only chains after ton.
