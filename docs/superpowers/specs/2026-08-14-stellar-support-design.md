# Stellar family support — design

Date: 2026-08-14
Status: approved (pre-implementation)

## Goal

Add first-class support for the **Stellar** family to nodecore
(`BlockchainType = "stellar"`), covering stellar mainnet and testnet, served
through both of Stellar's self-contained APIs: **stellar-rpc** (formerly
soroban-rpc, JSON-RPC 2.0) and **Horizon** (REST). There is no dshackle
surface to inherit — stellar was never actually served by dshackle — so the
method surface is simply the complete public API of each.

The chain registry is already in place: `pkg/chains/public/chains.yaml`
carries the `stellar` protocol (`type: stellar`, mainnet `Public Global
Stellar Network ; September 2015` / testnet `Test SDF Network ; September
2015` as chain-ids, grpcIds 1174/10208, `validate-peers: false`,
`expected-block-time: 5s`) and the generated `pkg/chains/chains_data.go`
already has `STELLAR` / `STELLAR_TESTNET`. No submodule bump, no
regeneration.

### Relationship to PR #295

An unmerged PR ([#295](https://github.com/drpcorg/nodecore/pull/295)) already
implements this family. It is used here as the source for the method surface
and for live-verified API shapes; the implementation is written fresh against
today's tree rather than rebased. This design deviates from it deliberately in
four places, each marked **[delta]** below:

- head comes from `getHealth` / the Horizon root document, not
  `getLatestLedger` / `GET /ledgers?order=desc&limit=1`, and block hashes are
  synthetic;
- lower bounds publish `StateBound` only, not `BlockBound` + `TxBound`;
- the generic `DecreasingBoundDetector` opt-out is not part of this work;
- health/settings validator gating follows the cosmos pattern
  (`ValidateSyncing` / `DisableChainValidation`) instead of re-checking flags
  the factory already honours.

## Scope

**In scope** — two APIs, deployable split (one upstream per API) or combined
(both connectors on one upstream):

- **stellar-rpc** (`json-rpc` connector): the full 12-method API; head and
  retention window from `getHealth`, passphrase chain validation from
  `getNetwork`, version labels from `getVersionInfo`.
- **Horizon** (`rest` connector): the full public REST surface (~50 path
  templates). Deprecated by SDF in favour of RPC but with no shutdown date,
  still on maintenance releases, and much of its query surface has no RPC
  equivalent. A standalone Horizon upstream is a single `rest` connector —
  legal by construction — and its accounting comes entirely from Horizon
  itself: head, chain validation, labels and lower bound all read the root
  document, health reads `GET /health`.

**Out of scope (YAGNI):**

- **Application errors carried inside a successful response.** `getTransaction`
  `NOT_FOUND`, `sendTransaction` `status ERROR|TRY_AGAIN_LATER|DUPLICATE`, and
  a failed `simulateTransaction` all pass through as successful results — an
  explicit decision for this pass, not an oversight. The general fix (splitting
  response shape from response verdict) is tracked separately.
- Horizon SSE streaming. `Accept: text/event-stream` is a per-request opt-in;
  without it every endpoint serves plain JSON, so the REST connector works
  untouched. An SSE request through nodecore will hang until the connector
  timeout — documented, follow-up with the WS/SSE machinery.
- Friendbot (a testnet 307 redirect to an external service, not node data) and
  Horizon's admin endpoints (a separate listener anyway).
- Cache policies and tag-parsers — every method is `cacheable: false` in v1.
- Broadcast dispatch for submissions. Both APIs are idempotent by envelope
  hash (RPC answers `DUPLICATE`, Horizon returns the stored result), but
  reconciliation is a follow-up.
- Retention-aware retry of `NOT_FOUND` / `-32600` range answers and Horizon's
  `before_history` 410 — node-local signals, meaningful only in mixed-depth
  pools.

## Verified API shapes

Recorded from production nodes during PR #295's live runs (stellar-rpc 27.1.1,
Horizon 27.0.0, 2026-07-18 and 2026-07-20). Not re-verified for this design —
see Testing.

- `getHealth {}` → `{"status":"healthy","latestLedger":63525714,
  "latestLedgerCloseTime":"1784332881","oldestLedger":63404755,
  "oldestLedgerCloseTime":"...","ledgerRetentionWindow":120960}`. The
  retention window (~7 days at ~5s/ledger) slides. **Unhealthy is a JSON-RPC
  error `-32603`, never a degraded result**: "data stores are not
  initialized..." while bootstrapping, "latency (Xs) since last known ledger
  closed is too high (>30s)" when stalled — the node polices its own staleness
  at a 30s default threshold.
- `getNetwork {}` → `{"passphrase":"Public Global Stellar Network ; September
  2015","protocolVersion":27}`. The passphrase is the registry chain-id, so
  validation is a string compare.
- `getVersionInfo {}` → `{"version":"27.1.1-<commit>","commitHash":...,
  "captiveCoreVersion":"stellar-core 27.1.0 (...)","protocolVersion":27}`.
- Horizon root `GET /` → a JSON document including `horizon_version`
  ("27.0.0-<commit>"), `network_passphrase`, `history_latest_ledger`,
  `history_elder_ledger`, `core_latest_ledger`.
- Horizon `GET /health` → `{"database_connected":true,"core_up":true,
  "core_synced":true}`, served with a `text/plain` Content-Type (harmless —
  `ResponseResult()` hands over raw bytes) and with HTTP 503 plus the same
  body when unhealthy.
- Horizon errors are RFC-7807 `problem+json` with the status mirrored in the
  body: `before_history` 410 is the retention miss, `stale_history` /
  `still_ingesting` 503 are node health.
- Unknown RPC method → standard `-32601`. Out-of-window range → `-32600`
  ("start ledger (N) must be between the oldest ledger: X and the latest
  ledger: Y for this rpc instance" — live-window numbers, never compare
  byte-exact).

Semantics that shape the design: SCP finality means a ledger is final on
close, strictly monotonic, no reorgs. `getLedgerEntries` serves **live state
only** — stellar-rpc holds no historical state at all.

## Architecture

### Plumbing

- `pkg/chains/chains.go`: add `Stellar BlockchainType = "stellar"`, accept it
  in `IsValidBlockchainType`, and add `case Stellar: return "stellar"` to
  `getMethodSpecName`.
- `internal/upstreams/upstream_factory.go`: `case chains.Stellar →
  stellar_specific.NewStellarChainSpecificObject(ctx, configuredChain,
  conf.Id, internalRequestConnector, conf.PollInterval, conf.Options)`.

### Flavor selection

`NewStellarChainSpecificObject` dispatches on the primary connector's type,
the way `cosmos_specific.NewCosmosSpecific` does: `specs.RestConnector` →
`StellarHorizonChainSpecificObject`, anything else →
`StellarRpcChainSpecificObject`.

The primary connector is `connectorsInfo.internalRequestConnector`, which
`upstream_factory.go` derives from `conf.GetBestConnector(config.DefaultMode)`
— **DefaultMode is hardcoded at that call site**, so it is always the
lowest-ordinal connector. With `JsonRpcConnector < RestConnector` in the
`ApiConnectorType` declaration order, a combined upstream is *always* driven
by stellar-rpc, in default and strict mode alike, and Horizon can never drive
accounting on a combined upstream. The upstream `mode` affects only the
`head-connector` default, and `blocks.createHead` uses the head connector's
type solely to choose poll vs ws-subscription — both stellar connectors are
HTTP, so `RpcHead` polls `chainSpecific.GetLatestBlock`, which uses the
chain-specific's own captured connector regardless.

No warning is logged for a combined upstream, and the dispatcher does not take
the connector list at all — it needs only the primary connector. Unlike TON,
whose v2 API and v3 indexer can legitimately sit in front of different
backends, stellar-rpc and Horizon are two front-ends of the same stellar node,
so combining them on one upstream is a normal deployment rather than a
misconfiguration worth warning about on every boot.

Open question, deliberately not encoded anywhere yet: in combined mode the
published `StateBound` comes from stellar-rpc's `oldestLedger` only, and
Horizon's `history_elder_ledger` is a *different* window (its own ingest DB, not
the rpc retention window). Whether combined upstreams should reconcile the two —
and which one should win — is a follow-up discussion, not something this pass
decides.

A shared `stellarBaseChainSpecificObject` (ctx, upstreamId, connector,
options, pollInterval, internalTimeout, labelsDelay, configuredChain) carries
what does not differ between flavors:

- `CapDetectors(caps.DetectorInput) → nil` — stellar-rpc has no websocket
  transport and Horizon streams SSE, not ws, so no ws-derived cap can ever be
  asserted.
- `MethodsProcessor() → nil` — neither API exposes a way to ask which methods
  a node implements, so upstreams keep the full method set their spec
  declares.
- `SubscribeHeadRequest` / `ParseSubscriptionBlock` → a shared
  `errUnsupportedHeadSubscriptions`; head tracking is poll-only.
- `BlockProcessor()` → `blocks.NewGenericBlockProcessor(ctx, upstreamId,
  pollInterval, internalTimeout, options.FinalizedBlockDetectionDisabled(),
  true, connector, chainSpecific)`.

### Head tracking **[delta]**

Poll-based `RpcHead` on both flavors, with **synthetic block hashes** derived
from the ledger sequence rather than the real ledger hash:

- **stellar-rpc**: `getHealth {}` → height `latestLedger`. `getLatestLedger`
  is deliberately not used internally — its response carries `headerXdr` and
  is far heavier than the head poll needs, and it exposes no parent hash
  either, so it buys nothing. It stays in the method spec for clients.
- **Horizon**: `GET /` root → height `history_latest_ledger`. This is the
  ledger range Horizon can actually serve, and the same document already
  feeds chain validation, labels and the lower bound.

Both flavors compute `hash = f(sequence)` and `parentHash = f(sequence-1)`
through one shared helper, so a chain supervisor holding a mix of rpc and
Horizon upstreams sees consistent, parent-linkable head hashes across them.
A sequence of `0` is rejected as a parse error (it would underflow the parent
and cannot occur on a live network).

`GetFinalizedBlock` = `GetLatestBlock` on both: SCP closes ledgers final, and
there is no "safe" ledger concept.

Consequence to accept: when stellar-rpc trips its own >30s staleness check,
`getHealth` fails with `-32603`, so the head stops advancing rather than
reporting a stale height. The health validator reads the same signal and marks
the upstream `Unavailable`, so it leaves the pool either way.

**Helper move.** `solana_specific.SyntheticHashes(slot, parentSlot)` moves to
`internal/upstreams/chains_specific/specific_helpers` unchanged (big-endian
uint64 in bytes `[0:8]` of a 32-byte id); solana calls the moved helper, so
its published hashes keep their exact current values. `aptos_specific` keeps
its own local `heightToHashId` (right-aligned encoding) — unifying it would
change no behavior for aptos but is out of scope here. Stellar uses the moved
helper.

### Shared node-document helpers

Every document more than one package reads lives in `specific_helpers`
(`specific_helpers/stellar.go`), next to the cosmos, polkadot and tendermint
helpers, following the `FetchX` / `ParseX` split those use:

- `StellarHealth` + `FetchStellarHealth` / `ParseStellarHealth` — read by the
  rpc head, the rpc health validator and the rpc lower-bound detector.
- `StellarHorizonRoot` + `FetchStellarHorizonRoot` / `ParseStellarHorizonRoot`
  — read by the Horizon head, chain validator, label detector and lower-bound
  detector.
- `StellarHorizonHealth` + `FetchStellarHorizonHealth` — read by the Horizon
  health validator; keeps the 503-body parsing so "core still syncing" stays
  distinguishable from "horizon is down".

Nothing cross-package lives under `stellar_validations`: that package holds
validators only. (Aptos currently exports `FetchLedgerInfo` from
`aptos_validations` and is consumed from `aptos_bounds` / `aptos_labels` — that
is the older, wrong shape, not a precedent to copy.) The passphrase fetch stays
private to `StellarChainValidator`, since only that validator reads
`getNetwork`.

### Chain validation

- `StellarChainValidator` (rpc): `getNetwork {}` → compare `passphrase`
  against `chain.ChainId` with `strings.EqualFold`. The registry loader
  lowercases every chain-id, so the compare has to be case-insensitive; the
  two passphrases differ in far more than case, so nothing is lost. Mismatch
  or empty passphrase → `FatalSettingError`; fetch failure → `SettingsError`.
- `StellarHorizonChainValidator`: same rules against `network_passphrase` from
  the root document.

Gated by `DisableChainValidation` in `SettingsValidators()`; the factory
already applies `DisableValidation` / `DisableSettingsValidation`.

### Health validation

- `StellarSyncingValidator` (rpc): `getHealth {}`. `status == "healthy"` →
  `Available`. An error whose text contains `not initialized` → `Syncing`
  (bootstrapping data stores). Any other error — including the node's own
  staleness rejection and transport failures — → `Unavailable`. A parsed
  result with a non-healthy status → `Unavailable`. No client-side clock math:
  the node polices its own staleness.
- `StellarHorizonSyncingValidator`: `GET /health`. All three booleans true →
  `Available`; `database_connected && core_up` but `core_synced == false` →
  `Syncing`; anything else → `Unavailable`. The 503 body is parsed for the
  booleans before falling back to the transport error, so "captive core still
  syncing" is distinguishable from "Horizon is down".

Gated by `*options.ValidateSyncing` in `HealthValidators()`, matching
`CosmosRestSpecific`; the factory already applies `DisableValidation` /
`DisableHealthValidation`.

### Labels

`client_version` + `client_type`, one detector per flavor, published every
`ValidationInterval * 5`:

- rpc: `getVersionInfo.version`, cut at the first `-` (`27.1.1-<commit>` →
  `27.1.1`), type `stellar-rpc` — the only production client.
- Horizon: `horizon_version` from the root document, same cut, type
  `horizon` — SDF's only implementation.

### Lower bounds **[delta]**

One detector per flavor, period 2 minutes (the window slides ~1 ledger/5s),
publishing **`protocol.StateBound` only**:

- rpc: `getHealth.oldestLedger`.
- Horizon: `history_elder_ledger` from the root document.

Zero or absent means the node did not report its boundary, not that full
history is available: return an error so the processor logs it, skips the tick
and keeps the previously cached bound. Each detector wraps its fetch in a
3-attempt / 500ms failsafe retry, as the other families' detectors do.

Rationale for STATE only: nothing in nodecore's own routing consults stellar
bounds (no tag-parsers means no matcher ever asks), so the bound exists to be
republished over the gRPC stream, where `emerald.lowerBoundTypeToApi` maps
`StateBound` → `LOWER_BOUND_STATE` — the bound dRPC's dispatcher consults for
this family. Adding `BlockBound` / `TxBound` later needs no wire change.

**No `DecreasingBoundDetector`.** Horizon's `history_elder_ledger` genuinely
can move *down* — `horizon db reingest range` backfills older ledgers into its
history DB, typically when a shallow deployment is being backfilled in the
background — but the shared monotonic filter in `BaseLowerBoundProcessor`
keeps the shallower value, which means nodecore *under-claims* history: it
routes away from an upstream that could have served the request, never towards
one that cannot. The filter's state is in-memory, so a restart republishes the
true bound. That is an acceptable trade for leaving shared bound-processing
code untouched. stellar-rpc needs nothing here — its `oldestLedger` only
climbs.

### Method specs

- `pkg/methods/specs/stellar-json-rpc.json` (`api-connectors: ["json-rpc"]`,
  type `plain`) — the complete 12-method API: `getHealth`, `getNetwork`,
  `getVersionInfo`, `getLatestLedger`, `getLedgers`, `getLedgerEntries`,
  `getEvents`, `getTransaction`, `getTransactions`, `getFeeStats`,
  `sendTransaction`, `simulateTransaction`.
- `pkg/methods/specs/stellar-horizon.json` (`api-connectors: ["rest"]`, type
  `plain`) — the public REST surface: root `GET#/`, `GET#/health`,
  `GET#/fee_stats`, accounts (+ `data/*`, offers, transactions, operations,
  payments, effects, trades), ledgers (+ transactions, operations, payments,
  effects), transactions (+ operations, payments, effects, `POST#/transactions`,
  `POST#/transactions_async`), operations (+ effects), payments, effects,
  offers (+ trades), `order_book`, trades, `trade_aggregations`, assets,
  claimable_balances (+ transactions, operations), liquidity_pools
  (+ transactions, operations, effects, trades), and paths
  (`strict-receive`, `strict-send`, plus the legacy `GET#/paths` alias).
- `pkg/methods/specs/stellar.json` — a `bundle` importing both.

Every method `cacheable: false`, default dispatch everywhere, no tag-parsers,
no aliases, no bans, no translations. One JSON field per line.

### Generic changes

Three, each needed by Horizon and each useful beyond it:

1. **`internal/server/http_server/handlers.go:48`** — `NewRestHandler`
   currently rejects any non-empty body that fails `sonic.Valid`. Horizon's
   `POST /transactions` takes `application/x-www-form-urlencoded`
   (`tx=<base64 XDR>`), so nodecore answers "no valid json" and never contacts
   the node. Enforce JSON validity only when the client's `Content-Type` is
   absent or contains `json`; other content types pass through opaquely.
2. **`internal/upstreams/connectors/http_connector.go:296`** —
   `applyConfigHeaders` unconditionally does
   `req.Header.Set("Content-Type", "application/json")`, then
   `applyClientHeaders` copies client headers with `req.Header.Add`, so a
   client-declared `Content-Type` lands as a *second* value and Go's transport
   writes two `Content-Type` lines. `Content-Type` is a singleton field and
   servers resolve it with `Header.Get` (first value), so Horizon sees
   `application/json` for a form body, `ParseForm` declines it, and the
   submission comes back `400 transaction_malformed`. Fix: in the client-header
   loop, treat `Content-Type` as single-valued — `req.Header.Set(k, vs[0])`
   and continue. The `additionalHeaders` precedence check runs first, so a
   `Content-Type` pinned in connector config still wins over the client's.
   Note the blast radius is REST only: `applyClientHeaders` is called only
   from `sendRest`, and the bug bites only when the client's value differs
   from the default (a client sending `application/json` produces two
   identical lines today, malformed but harmless — which is why it went
   unnoticed).
3. **`internal/server/http_server/http_server.go:116`** — `reqType :=
   Ternary(len(restPath) > 0, Rest, JsonRpc)` makes `/queries/stellar/` a
   JSON-RPC request, so Horizon's root is reachable only as the double-slash
   `/queries/stellar//`. Change the rule to: empty rest path **and** HTTP
   `GET` → `Rest` with path `/`. A JSON-RPC call is always a POST, so nothing
   legitimate changes; the visible difference is that a stray
   `GET /queries/eth` now answers with a REST-shaped "method not supported"
   error instead of a JSON-RPC parse error.

Internal probes are unaffected by all three — they build `GET#/` requests
directly through the connector.

### Docs

Table-row and prose additions only; no per-chain deployment section.

- `README.md` — the two chain-family lists.
- `docs/nodecore/11-method-specs.md` — the bundle table (`stellar` →
  `stellar-json-rpc`, `stellar-horizon`) and the plain-spec tables
  (`json-rpc` and `rest` rows).
- `docs/nodecore/05-upstream-config.md` — the `validate-syncing` /
  `validate-peers` prose, plus one row each in the chain-validator, health
  validator, lower-bound detector and client-label detector tables.

## Testing

Unit tests only, against mocked connectors — a deliberate choice for this
pass. Table-driven, per component:

1. **Head**: rpc `getHealth` → height + synthetic hash pair, parent linkage
   (`block(N).ParentHash == block(N-1).Hash`); Horizon root →
   `history_latest_ledger` + the same hashes for the same sequence; sequence 0,
   absent field, and unparseable body → error; `GetFinalizedBlock` ==
   `GetLatestBlock`.
2. **Helper move**: `specific_helpers.SyntheticHashes` reproduces solana's
   current output for the values already asserted in
   `solana_chain_specific_test.go`.
3. **Chain validation**: mainnet vs testnet passphrase cross-check both ways,
   case-insensitive match against the lowercased registry chain-id, empty
   passphrase → fatal, fetch error → `SettingsError` (both flavors).
4. **Health**: rpc — healthy, `not initialized` error → `Syncing`, staleness
   error → `Unavailable`, transport error → `Unavailable`, non-healthy status
   → `Unavailable`. Horizon — all-true → `Available`, `core_synced=false` →
   `Syncing`, db/core down → `Unavailable`, 503-with-body parsed for the
   booleans, unparseable error → `Unavailable`.
5. **Labels**: version truncation at the first `-` for both flavors, client
   types `stellar-rpc` / `horizon`, unparseable payload → error.
6. **Bounds**: value → a single `StateBound` datum, zero/absent → error,
   fetch error → error (both flavors).
7. **Flavor dispatch**: `rest` connector → the Horizon object, `json-rpc` →
   the rpc object, nil connector → the rpc object without panicking.
8. **Routing**: empty rest path + `GET` → `Rest` with path `/`; empty rest
   path + `POST` → `JsonRpc` (unchanged).
9. **REST body validation**: form-urlencoded body accepted, declared-JSON
   invalid body rejected, undeclared invalid body rejected.
10. **Client headers**: a client `Content-Type` replaces the default and
    exactly one value reaches the request; a config-pinned `Content-Type`
    still wins; other multi-valued headers still stack.
11. **Specs**: `stellar` bundle loads and resolves both imports in
    `pkg/methods/methods_spec_test.go`.

### Manual live checklist

Three things unit tests cannot prove, to be checked against real nodes later.
Fixes (1) and (2) below must be verified together — with only one applied the
submission still fails.

```
# (1) non-JSON body reaches the upstream, (2) exactly one Content-Type on the wire
curl -s -X POST 'http://localhost:9090/queries/stellar/transactions' \
  -H 'Content-Type: application/x-www-form-urlencoded' --data 'tx=AAAA'
# broken: nodecore's own "no valid json" error, or Horizon's 400 with the
#         wrong reason because it parsed the body as JSON
# fixed:  Horizon's own 400 transaction_malformed problem+json, byte-exact
```

Repeat against `POST /queries/stellar/transactions_async`. Then check the
routing change with `curl 'http://localhost:9090/queries/stellar/'` — it must
return Horizon's root document, not a JSON-RPC parse error.

Worth a byte-exactness spot check while there: a closed-range
`GET /queries/stellar/ledgers?order=desc&limit=2`, a `getTransaction` for a
fabricated hash (`NOT_FOUND` result passthrough), an out-of-window
`GET /queries/stellar/ledgers/1` (410 `before_history` passthrough), and the
published `STATE` bound against the node's live `oldestLedger` /
`history_elder_ledger`.

## Out of scope / follow-ups

- In-result application errors (failed `simulateTransaction`,
  `sendTransaction status ERROR`, `getTransaction NOT_FOUND`) — the general
  shape-vs-verdict split, tracked separately.
- Horizon 4xx responses become a `ResponseError` and count against the
  upstream in dimensions and rating even when the client's request was simply
  wrong. This is pre-existing generic REST behavior shared with cosmos-rest
  and TON, not introduced here, and is left alone.
- `DecreasingBoundDetector`, if Horizon backfills turn out to be routine.
- Horizon SSE streaming, once WS/SSE machinery exists.
- Broadcast `sendTransaction` with `DUPLICATE`-as-success reconciliation.
- Retention-aware routing/retry for `NOT_FOUND`, `-32600` range errors and
  `before_history` 410 in mixed-depth pools.
- `BlockBound` / `TxBound` alongside `StateBound` if the dispatcher starts
  consulting them.
