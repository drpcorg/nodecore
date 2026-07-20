# Stellar family support — design

Date: 2026-07-18
Status: approved (pre-implementation)

## Goal

Add first-class support for the **Stellar** blockchain family to nodecore
(`BlockchainType = "stellar"`), covering stellar mainnet and testnet, served
via **stellar-rpc** (formerly soroban-rpc). Unlike the previous five
families there is no dshackle surface to inherit: stellar was never actually
served by dshackle (the include-list entry is dead — dshackle has no stellar
chain), so the method surface is simply the complete stellar-rpc API.

Depends on the registry addition in drpcorg/public (stellar protocol entry,
chain-ids = network passphrases, grpcIds 1169/10204); this branch pins the
submodule to that commit and the PR lands after the registry merges.

## Scope

- **In scope:** two self-contained Stellar APIs, deployed in split or
  combined mode (one upstream per API, or both connectors on one upstream):
  - **stellar-rpc** (JSON-RPC 2.0, `json-rpc` connector): the full 12-method
    API; head via `getLatestLedger`, passphrase chain validation, getHealth
    health, version labels, retention-window lower bounds.
  - **Horizon** (`rest` connector): the full public REST surface (~45 path
    templates — deprecated by SDF in favor of RPC but with no shutdown date,
    still on maintenance releases, and much of its query surface has NO RPC
    equivalent). Standalone horizon upstream = single `rest` connector (a
    plain type, legal by construction); its accounting comes entirely from
    Horizon itself: head via `GET /ledgers?order=desc&limit=1`
    (hash+sequence), health via `GET /health` (database_connected / core_up
    / core_synced), chain validation via the root `network_passphrase`,
    labels from `horizon_version`, and lower bounds from
    `history_elder_ledger` — which moves **both ways** (retention reaper up,
    `db reingest range` backfill down) and therefore opts into
    `DecreasingBoundDetector`.
  - Combined (json-rpc + rest on one upstream): accounting follows the
    primary connector (json-rpc in default mode), the other connector serves
    methods only, and a warning is logged.
- **Out of scope (YAGNI):** Horizon SSE streaming (`Accept:
  text/event-stream` is per-request opt-in; without it every endpoint serves
  plain JSON, so the REST connector works untouched — an SSE request through
  nodecore will just hang until the connector timeout; documented, follow-up
  with WS/SSE machinery); friendbot (a testnet 307 redirect to an external
  service, not node data); admin endpoints (separate listener anyway); cache
  tag-parsers (`cacheable: false` v1); broadcast dispatch for submissions
  (both APIs are idempotent by envelope hash — RPC answers `DUPLICATE`,
  Horizon returns the stored result — but reconciliation is a follow-up);
  retention-aware retry of `NOT_FOUND`/`before_history` (410) answers
  (node-local, meaningful only with mixed-depth pools — Horizon's
  `before_history` is the ready-made retry-on-deeper-node signal for that
  follow-up).

## Approach

The cleanest family yet: standard JSON-RPC 2.0, standard error codes, and
one method (`getHealth`) that hands over health, staleness *and* the data
window in a single call.

### Verified live API shapes (from our production nodes, stellar-rpc 27.1.1)

- `getHealth {}` → `{"status":"healthy","latestLedger":63525714,
  "latestLedgerCloseTime":"1784332881","oldestLedger":63404755,
  "oldestLedgerCloseTime":"...","ledgerRetentionWindow":120960}` — the
  retention window (~7 days at ~5s/ledger) slides; **unhealthy is a JSON-RPC
  error `-32603`**, never a degraded result: "data stores are not
  initialized..." while bootstrapping, "latency (Xs) since last known ledger
  closed is too high (>30s)" when stalled (the node polices its own
  staleness, default threshold 30s).
- `getNetwork {}` → `{"passphrase":"Public Global Stellar Network ;
  September 2015","protocolVersion":27}` (testnet: "Test SDF Network ;
  September 2015", both verified live) — the passphrase is the chain-id in
  the registry, so validation is a direct string compare.
- `getLatestLedger {}` → `{"id":"<hex hash>","sequence":63525769,
  "closeTime":"1784333187","headerXdr":...}`. SCP finality: ledgers are
  final on close, strictly monotonic, no reorgs.
- `getVersionInfo {}` → `{"version":"27.1.1-<commit>","commitHash":...,
  "captiveCoreVersion":"stellar-core 27.1.0 (...)","protocolVersion":27}`.
- Unknown method → standard `-32601 "method not found"`.

### Semantics that shape the design (from the v27.1.1 source)

- Errors are the standard five JSON-RPC codes only. `-32602` is
  deterministic; `-32600` range errors ("startLedger must be within the
  ledger range: X - Y") and `-32603` are **node-local** (retention/DB) —
  with a single node per network v1 does nothing special about them.
- `getTransaction` **not-found is a successful result**
  (`{"status":"NOT_FOUND",...}`), ambiguous between never-landed and
  aged-out; passthrough in v1.
- `sendTransaction` is async enqueue; core rejections come back as a
  *result* with `status ERROR|TRY_AGAIN_LATER|DUPLICATE|PENDING` —
  passthrough, no error surgery needed.
- `getLedgerEntries` serves **live state only** — there is no historical
  state on stellar-rpc at all.

## Architecture

### Factory and type plumbing

- `chains.go`: add `Stellar BlockchainType = "stellar"`, accept in
  `IsValidBlockchainType`, `case Stellar: return "stellar"` in
  `getMethodSpecName`. STELLAR/STELLAR_TESTNET constants regenerate from the
  bumped registry submodule.
- `upstream_factory.go`: `case chains.Stellar` →
  `stellar_specific.NewStellarChainSpecificObject(...)`.

### Head tracking (`stellar_specific`)

Poll-based `RpcHead`: `getLatestLedger {}` → height `sequence`, hash `id`
(hex). Parent hash EmptyHash (not exposed; SCP has no reorgs so nothing
consumes it). `GetFinalizedBlock` = `GetLatestBlock` (close == finality).
`BlockProcessor()` nil; subscriptions unsupported.

### Chain validation

`StellarChainValidator`: `getNetwork {}` → `passphrase` compared (exact,
case-sensitive — passphrases are canonical strings) against
`chain.ChainId`. Note: the registry loader lowercases every chain-id globally, so the compare is case-insensitive (EqualFold) in practice — safe, the two passphrases differ in far more than case. Mismatch → FatalSettingError; fetch error → SettingsError.

### Health validation

`StellarHealthValidator`: `getHealth {}`:
- success (`status == "healthy"`) → Available;
- JSON-RPC error whose message mentions the uninitialized data stores →
  Syncing (bootstrapping);
- any other error (incl. the >30s staleness rejection) → Unavailable.
The node polices head staleness itself — no client-side clock math needed.

### Labels and lower bounds

- `client_version` from `getVersionInfo.version` truncated at the first
  `-` (`27.1.1-<commit>` → `27.1.1`), client type `stellar-rpc`.
- `StellarLowerBoundDetector`: `getHealth.oldestLedger` → emitted as BLOCK
  and TX bounds (getLedgers/getTransactions/getTransaction/getEvents all
  serve `[oldestLedger, latestLedger]`). No STATE bound — state is
  live-only. Period 2 min (the window slides ~1 ledger/5s). The bound only
  climbs; no decrease opt-in needed.

### Method spec (`pkg/methods/specs/stellar.json`)

The complete 12-method stellar-rpc API, all `cacheable: false` in v1,
default dispatch everywhere (see Scope for why sendTransaction is not
broadcast yet):

getHealth, getNetwork, getVersionInfo, getLatestLedger, getLedgers,
getLedgerEntries, getEvents, getTransaction, getTransactions, getFeeStats,
sendTransaction, simulateTransaction.

A second spec `stellar-horizon.json` (`api-connectors: ["rest"]`) carries the
Horizon REST surface: root `GET#/`, `GET#/health`, `GET#/fee_stats`,
accounts (+ transactions/operations/payments/effects/trades/offers/data
sub-resources), ledgers (+subs), transactions (+subs, `POST#/transactions`,
`POST#/transactions_async`), operations/payments/effects, offers (+trades),
order_book, trades, trade_aggregations, assets, claimable_balances (+subs),
liquidity_pools (+subs), paths (strict-receive/strict-send + the legacy
`GET#/paths` alias). Horizon errors are RFC-7807 `problem+json` with the
status mirrored in the body — nodecore's REST error path classifies them
as-is and passes bodies through byte-exact (`before_history` 410 = the
node-local retention miss; `stale_history`/`still_ingesting` 503 = node
health). The `stellar.json` bundle imports both specs.

No translations, no bans, no aliases, no envelope work.

## Testing

1. **Unit tests** (family parity): latest-ledger head parsing, passphrase
   validation mainnet-vs-testnet cross-check, health mapping (healthy /
   uninitialized → Syncing / staleness error → Unavailable / transport
   error), version label truncation, oldestLedger bounds, method-spec
   loading.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   locally against both production nodes over VPN. Corpus: all 12 methods
   node-direct vs nodecore — getLedgers/getTransactions at fixed closed
   ranges (byte-exact modulo the node-local envelope fields latestLedger*/
   cursor — compare the data arrays exactly), getTransaction for a real
   included tx (byte-exact) and a fabricated hash (NOT_FOUND result
   passthrough), out-of-window range → `-32600` passthrough, invalid params
   → `-32602`, unknown method, getLedgerEntries for a known entry,
   getEvents closed range, simulateTransaction with a deterministically
   invalid envelope (error-in-result passthrough), `sendTransaction` only
   with an invalid envelope (`-32602 invalid_xdr` passthrough, nothing
   enqueued).
   **Executed 2026-07-18 — passed.** 34 PASS / 0 FAIL / 2 SKIP (the
   anticipated valid-LedgerKey XDR case) across mainnet and testnet:
   ledgers/transactions/events arrays byte-exact (node-local envelope
   fields stripped), NOT_FOUND and error-in-result simulateTransaction
   passthroughs identical, invalid-XDR sendTransaction -32602 identical,
   passphrases and retention windows verified. Live corrections: v27's
   out-of-window message reads "start ledger (N) must be between the oldest
   ledger: X and the latest ledger: Y for this rpc instance" (live-window
   numbers — never compare byte-exact), and `getEvents` does accept
   `endLedger`. Bounds published live (BLOCK=TX=oldestLedger).
   **Horizon flavor executed 2026-07-20 against our own testnet Horizon
   (27.0.0, ~420-ledger history window) — 20 PASS / 2 FAIL of 22.** Upstream
   AVAILABLE, bounds BLOCK=TX=3705280 == live `history_elder_ledger`, head
   parsed. Byte-exact through nodecore: ledgers list records, ledger-by-seq
   + its transactions/operations/effects/payments, tx-by-hash +
   operations/effects, account transactions/operations, health,
   before-window `GET /ledgers/1` → 410 `before_history` problem+json,
   `GET /accounts/GINVALID` → 400, all-zeros tx hash → 404, same-asset
   order_book (200), bad trade_aggregations (400); root/fee_stats/accounts
   shape-identical (live state). The two initial FAILs — `POST
   /transactions` and `/transactions_async` — exposed two generic REST-path
   bugs, both fixed and re-verified live the same day: RestHandler rejected
   non-empty non-JSON bodies (now JSON validity is only enforced when the
   Content-Type says/implies JSON, so form-urlencoded submissions pass
   through opaquely), and the connector's default `Content-Type:
   application/json` stacked ahead of the client's value (client
   Content-Type now replaces the default). After the fixes both submissions
   pass through byte-exact (Horizon's own `transaction_malformed` 400).
   Remaining quirk (follow-up): Horizon root is only reachable as
   `/queries/{chain}//` — an empty rest path falls through to the JSON-RPC
   parser.
   **Mainnet Horizon re-run 2026-07-20 (27.0.0, ~1350-ledger window,
   30-day backfill in progress) — 22 PASS / 0 FAIL**, same corpus with REF
   = latest−500; bounds BLOCK=TX=63563520 == live `history_elder_ledger`
   fetched the same minute (elder held steady throughout the run, so no
   decrease-republish was exercised live), and both POST
   `/transactions{,_async}` now byte-exact (`transaction_malformed` 400)
   with the fixed binary.

3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- Broadcast `sendTransaction` with DUPLICATE-as-success reconciliation.
- Retention-aware routing/retry for NOT_FOUND and `-32600` range errors in
  mixed-depth pools.
- This is the last planned family: every chain our fleet runs is then
  servable by nodecore.
