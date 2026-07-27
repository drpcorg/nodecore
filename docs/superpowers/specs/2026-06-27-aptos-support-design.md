# Aptos blockchain support — design

Date: 2026-06-27
Status: approved (pre-implementation)

## Goal

Add first-class support for the **Aptos** blockchain to nodecore as a new
blockchain family (`BlockchainType = "aptos"`), covering the Aptos Node REST
API for **mainnet** and **testnet**, with full gRPC (emerald) API support.

## Scope

- **In scope:** Aptos Node REST API (fullnode) — `GET /v1` and friends; mainnet
  + testnet; routing, head tracking, health/chain validation, client labels,
  lower-bound detection; gRPC API support (ChainRef in the emerald proto).
- **Out of scope (YAGNI):** Aptos Indexer GraphQL API; devnet; WebSocket
  subscriptions (Aptos REST has none).

## Approach

Aptos is modelled as a **REST-based blockchain family**, following the existing
**Algorand** family as the closest template. A runtime-only extension
(`LoadExtraChains` + extra method-spec FS) is **not** viable for a brand-new
family because `IsValidBlockchainType`, the `getChainSpecific` factory switch,
and `getMethodSpecName` are compile-time constructs; introducing a new family
with its own chain-specific logic requires Go code changes. This is consistent
with how Algorand and Aztec already live in the tree.

Aptos's REST API is simpler than Algorand's: a single endpoint `GET /v1`
(ledger info / index) returns everything needed for head, lower bound, chain id
and client labels.

### Verified Aptos REST API shapes (from a live mainnet fullnode)

`GET /v1` (ledger info / index) — note: all numeric fields are JSON **strings**
(U64), `chain_id` is a number:

```json
{
  "chain_id": 1,
  "epoch": "16331",
  "ledger_version": "5965411071",
  "oldest_ledger_version": "0",
  "ledger_timestamp": "1782581442052319",
  "node_role": "full_node",
  "oldest_block_height": "0",
  "block_height": "860298804",
  "git_hash": "ce732f6fcb5ce034d927a8d3b9c0d0b28d207e63"
}
```

`GET /v1/-/healthy?duration_secs=N` → HTTP 200 `{"message":"aptos-node:ok"}`
when the node has committed within `N` seconds; HTTP 503 otherwise.

`GET /v1/blocks/by_height/{h}?with_transactions=false`:

```json
{"block_height":"1","block_hash":"0x014e30...","block_timestamp":"1665609760857472",
 "first_version":"1","last_version":"1","transactions":null}
```

## Networks & identifiers (verified free / non-colliding)

| short-names                 | chain-id | net-version | grpcId / ChainRef | node chain_id |
|-----------------------------|----------|-------------|-------------------|---------------|
| `aptos`, `aptos-mainnet`    | `0x1`    | `1`         | `1168`            | 1             |
| `aptos-testnet`             | `0x2`    | `2`         | `10203`           | 2             |

- chain-id uses Aptos's **real** chain_id (no sentinel) — enabled by the
  `GetChainByChainIdAndVersion` change below, which scopes uniqueness to
  `(blockchain-type, chain-id, net-version)`. `0x1` no longer collides with
  Ethereum because they are different blockchain types.
- grpcId values `1168` / `10203` verified unused in `emerald-grpc/proto/common.proto`
  and follow the existing convention (mainnet ~11xx, testnet ~102xx). dRPC's
  canonical `chains.yaml` (drpcorg/public) has no Aptos entry, so there is no
  upstream allocation to reconcile against today.

## Refactor: scope chain-id uniqueness to blockchain type

`GetChainByChainIdAndVersion` currently keys a reverse lookup on
`(chain-id, net-version)` globally. It has exactly one caller —
`eth_validations/eth_chain_validator.go` — used only to build a better error
message. No map is keyed by chain-id; no other code assumes global chain-id
uniqueness. We add a blockchain-type parameter so uniqueness is per-type.

`pkg/chains/chains.go`:

```go
func GetChainByChainIdAndVersion(blockchainType BlockchainType, chainId, netVersion string) *ConfiguredChain {
	for _, chain := range chains {
		if chain.Type == blockchainType && chain.ChainId == chainId && chain.NetVersion == netVersion {
			return chain
		}
	}
	return UnknownChain
}
```

`internal/upstreams/validations/eth_validations/eth_chain_validator.go` (single
caller) passes `c.chain.Type`:

```go
actualChain := chains.GetChainByChainIdAndVersion(c.chain.Type, chainId, netVersion)
```

This is the only behavioural change to existing chains, and it is semantically
neutral for them (the EVM validator's `c.chain.Type` is always `eth`).

## Files

### Data / codegen (protoc + go available; regeneration works)

1. **`pkg/chains/public/chains.yaml`** (submodule) — add a `type: aptos`
   protocol block with mainnet + testnet chains (table above), plus
   `expected-block-time` and `lags` settings.
2. **`emerald-grpc/proto/common.proto`** (submodule) — add
   `CHAIN_APTOS__MAINNET = 1168;` and `CHAIN_APTOS__TESTNET = 10203;` to the
   `ChainRef` enum.
3. **`pkg/chains/chains_data.go`** — regenerate via `make generate-networks`
   (adds `APTOS`, `APTOS_TESTNET` consts, `chainsMap` entries, `String()` cases).
4. **`pkg/dshackle/common.pb.go`** — regenerate via `make dshackle-proto-gen`.

### Blockchain type

5. **`pkg/chains/chains.go`** —
   - add `Aptos BlockchainType = "aptos"` to the const block;
   - add `Aptos` to `IsValidBlockchainType`;
   - add `case Aptos: return "aptos"` to `getMethodSpecName`;
   - the `GetChainByChainIdAndVersion` refactor above.

### Method spec

6. **`pkg/methods/specs/aptos.json`** — `spec.name: "aptos"`,
   `api-connectors: ["rest"]`, `type: "plain"`. Methods are the REST routes used
   both by clients and by internal detectors:
   - `GET#/v1` (ledger info; not cacheable)
   - `GET#/v1/-/healthy` (not cacheable)
   - `GET#/v1/blocks/by_height/*`, `GET#/v1/blocks/by_version/*`
   - `GET#/v1/accounts/*`, `GET#/v1/accounts/*/resources`, `GET#/v1/accounts/*/modules`
   - `GET#/v1/transactions`, `GET#/v1/transactions/by_hash/*`, `GET#/v1/transactions/by_version/*`
   - `POST#/v1/transactions` (submit; not cacheable)
   - `POST#/v1/view`
   - `GET#/v1/estimate_gas_price`
   - `GET#/v1/events/*`
   - `GET#/v1/blocks/by_height/*/hash` is NOT a real Aptos route; block hash
     comes from the by_height body (see GetLatestBlock).

   (The incoming-REST router `rest_parser.go` is generic — it matches against
   the method spec via `specs.MatchRestMethod` — so no parser change is needed.)

### Chain-specific implementation (main work, templated on `algorand_specific`)

7. **`internal/upstreams/chains_specific/aptos_specific/aptos_chain_specific.go`**
   (+ `_test.go`) — implements `chains_specific.ChainSpecific`.
8. **`internal/upstreams/validations/aptos_validations/`**
   - `aptos_health_validator.go` (+ `_test.go`)
   - `aptos_chain_validator.go` (+ `_test.go`)
9. **`internal/upstreams/labels/aptos_labels/aptos_detectors.go`** (+ `_test.go`)
10. **`internal/upstreams/lower_bounds/aptos_bounds/aptos_lower_bound.go`**
    (+ `_test.go`)

### Wiring & test helpers

11. **`internal/upstreams/upstream_factory.go`** — add
    `case chains.Aptos:` to `getChainSpecific`, constructing the Aptos object
    with the internal request connector (same shape as the Algorand case).
12. **`pkg/test_utils/test_helpers.go`** — add `NewAptosChainSpecific` helper
    (mirrors `NewAlgorandChainSpecific`).

## `ChainSpecific` behaviour (data flow)

- **GetLatestBlock(ctx):** `GET /v1` → `block_height`; then
  `GET /v1/blocks/by_height/{block_height}?with_transactions=false` →
  `block_hash`. Parent block id is a deterministic encoding of `block_height-1`
  (Aptos's by_height body carries no parent hash; this mirrors Algorand's
  fixed-width fallback so HeadEvents always carry non-empty ids). Returns
  `protocol.NewBlock(block_height, ledger_version, hash, parentHash)`.
- **GetFinalizedBlock(ctx):** identical to `GetLatestBlock` — Aptos has
  deterministic BFT finality, so the latest committed block is final (same
  pattern Algorand uses).
- **ParseBlock(bytes):** parse a `/v1` payload → `block_height`
  (`protocol.NewBlock(height, 0, EmptyHash, EmptyHash)`); error on `0`.
- **ParseSubscriptionBlock / SubscribeHeadRequest:** return an error — Aptos
  REST has no WebSocket subscriptions.
- **HealthValidators:** `AptosHealthValidator` issues
  `GET /v1/-/healthy?duration_secs=<threshold>`. HTTP 200 → `Available`;
  HTTP 503 → `Syncing`; transport/other error → `Unavailable`. Honours
  `DisableHealthValidation`.
- **SettingsValidators:** `AptosChainValidator` issues `GET /v1`, parses
  `chain_id` (int) and compares it to the configured chain-id (`0x1`→1, `0x2`→2).
  Match → `Valid`; mismatch → `FatalSettingError`; fetch failure →
  `SettingsError`. Skips when `ChainId == ""` or `DisableChainValidation`. No
  mapping table needed (simpler than Algorand).
- **LabelsProcessor:** client-label detector issues `GET /v1`; client type
  `"aptos-node"`, version derived from `git_hash` (short), plus `node_role`.
  Wrapped in the standard `NewClientLabelDetectorHandler` + `BaseLabelsProcessor`.
- **LowerBoundProcessor:** `AptosLowerBoundDetector` issues `GET /v1` and emits
  bounds **directly** from `oldest_ledger_version` (StateBound) and
  `oldest_block_height` (BlockBound) — no binary search (unlike Algorand).
  Fallback on failure: re-emit cached bound, else `UnknownBound=0`.
- **CapDetectors:** `caps.DefaultCapDetectors(upstreamId, input.WsConnector)`
  (same as Algorand).
- **BlockProcessor:** `nil`.

## Error handling

- Aptos U64 fields are JSON **strings**; parse with `json:",string"` tags or
  `strconv.ParseUint`. `block_height == 0` from `/v1` → treat as unavailable.
- REST errors surface via `response.HasError()` / `response.ResponseCode()`
  (e.g. 503 from the health endpoint), consistent with Algorand's bounds code.
- Lower-bound failures never hard-fail: retain cached bound or emit
  `UnknownBound`.

## Chain settings (defaults; tunable)

- `expected-block-time: 1s` — Aptos produces sub-second blocks; `1s` is a safe
  conservative value for `AverageRemoveSpeed` / lag math (revisit after observing
  real head-lag behaviour).
- `lags: { syncing: 60, lagging: 20 }` — placeholder thresholds in blocks;
  tunable.
- `options: { validate-peers: false }` — Aptos exposes no peer-set endpoint
  (matches Algorand).

## Testing

- Unit tests for every new component (chain-specific, both validators, labels,
  bounds), each with a mocked `ApiConnector` fed the real Aptos payloads
  captured above. Mirrors the existing Algorand `_test.go` files.
- Existing-behaviour guard: build + full test suite still green after the
  `GetChainByChainIdAndVersion` signature change.
- Final gate: `make generate-networks && make dshackle-proto-gen &&
  go build ./... && go test ./...`.

## Open items (defaulted, not blocking)

- `expected-block-time` / `lags` defaults above are first-pass estimates.
- grpcId `1168` / `10203` chosen as next-free; if dRPC later allocates canonical
  Aptos ids upstream, reconcile then.
