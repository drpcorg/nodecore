# TON family support — design

Date: 2026-07-17
Status: draft (pending approval)

## Goal

Add first-class support for the **TON** blockchain family to nodecore
(`BlockchainType = "ton"`), covering ton mainnet (testnet is registered in
`chains.yaml` and gets family support for free), with full gRPC (emerald)
API support, so the chain can be served by nodecore instead of dshackle.

## Scope

- **In scope:** toncenter-style REST upstreams with **two API generations on
  two connectors**: the v2 HTTP API (`rest` connector, base URL carries the
  `/api/v2` prefix) and the v3 indexer (`rest-additional` connector) — the
  same two-connector mechanism the bitcoin family uses for esplora; the
  dshackle method surface (v2 REST + `POST /jsonRPC` wrapper + `/api/v3/*`);
  poll-based head tracking; race-free chain validation via zerostate hashes;
  liveness health; client labels from the v2 OpenAPI document; archival
  probe lower bounds.
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
3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- Binary-search history-depth detection for non-archival liteservers.
- v3-indexer staleness vs v2 head divergence signal.
- `gen_utime` freshness health probe.
- Broadcast dispatch for sendBoc (idempotent, but needs "already seen"
  reconciliation).
- celestia/stellar need `chains.yaml` registry entries first; they are the
  last dshackle-only chains after ton.
