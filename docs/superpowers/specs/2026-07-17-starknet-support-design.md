# Starknet family support — design

Date: 2026-07-17
Status: draft (pending approval)

## Goal

Add first-class support for the **Starknet** blockchain family to nodecore
(`BlockchainType = "starknet"`), covering starknet mainnet and sepolia, with
full gRPC (emerald) API support, so these chains can be served by nodecore
instead of dshackle. This also fixes the current half-registered state where
a starknet upstream fails with `no method spec with name ''`.

## Scope

- **In scope:** juno/pathfinder JSON-RPC 2.0 upstreams (plain HTTP, no auth,
  root path); the 24-method surface currently served by dshackle; poll-based
  head tracking; real chain-id validation (dshackle has none for starknet);
  syncing health; juno/pathfinder client labels; lower bounds.
- **Out of scope (YAGNI):** WebSocket subscriptions (juno is in
  `CLIENTS_WITHOUT_WS`; spec-v0.8 WS methods are a follow-up); pinning to a
  versioned RPC path (`/v0_8` etc. — dshackle serves the root path today and
  parity wins; the root drifts with juno upgrades, worth revisiting at
  rollout); cache tag-parsers (`cacheable: false` v1); pathfinder deployment
  concerns (we run juno only; pathfinder label detection is kept because it
  is free and dshackle has it).

## Approach

Starknet is the most standard family so far: plain JSON-RPC 2.0 with proper
top-level errors, no envelope quirks, no translations. It follows the
near/bitcoin template directly.

### Verified live API shapes (from our production sepolia node, juno v0.16.4)

- `starknet_chainId []` → `"0x534e5f5345504f4c4941"` (hex-encoded ASCII
  `SN_SEPOLIA`); mainnet is `0x534e5f4d41494e` (`SN_MAIN`) — **exactly the
  `chain-id` values already in `chains.yaml`**, so validation is a direct
  case-insensitive string compare.
- `starknet_specVersion []` → `"0.10.2"` at the root path; juno also serves
  pinned `/v0_8`, `/v0_9`, `/rpc/v0_X` endpoints. dshackle uses the root.
- `starknet_blockHashAndNumber []` → `{"block_hash":"0x...",
  "block_number":12107320}`.
- `starknet_getBlockWithTxHashes ["latest"]` → flat object with
  `block_hash`, `parent_hash`, `block_number`, `timestamp`,
  `status:"ACCEPTED_ON_L2"`, `starknet_version`, gas-price fields,
  `transactions:[hashes]`.
- `starknet_syncing []` → bare `false` when synced; a
  `{starting/current/highest_block_num...}` object while syncing.
- `juno_version []` → `"v0.16.4"`; `pathfinder_version` → `-32601` on juno
  (and vice versa on pathfinder) — this pair is the client-identity probe.
- Errors are standard JSON-RPC: `{"code":24,"message":"Block not found"}`,
  `-32601 "Method Not Found"`, `-32602 "Invalid Params"` with a `data`
  string. No XRPL-style weirdness.
- Our juno keeps **full history and historical state by default** (block 1
  and old `getStorageAt` both live) — archive-grade, no pruning flags in the
  role.

## Architecture

### Factory and type plumbing

- `chains.go`: `getMethodSpecName` gains `case Starknet: return "starknet"`
  (the constant and `IsValidBlockchainType` acceptance already exist — that
  is precisely why the upstream currently gets to `NewUpstreamMethods` and
  dies on the empty spec name).
- `upstream_factory.go`: `case chains.Starknet` →
  `starknet_specific.NewStarknetChainSpecificObject(...)`.
- `chains.yaml` entries (mainnet `0x534e5f4d41494e` grpcId 1014, sepolia,
  20s block time, lags 5/1) and gRPC ChainRefs already exist.

### Head tracking (`starknet_specific`)

Poll-based `RpcHead`: `starknet_getBlockWithTxHashes ["latest"]` (dshackle
parity) → `block_number`, `block_hash`, `parent_hash`. `GetFinalizedBlock` =
`GetLatestBlock` in v1 (an `l1_accepted` head is a possible follow-up — the
spec supports the tag, but dshackle never exposed a distinct finalized head
for starknet and no consumer asks for one). `BlockProcessor()` nil;
subscriptions unsupported (poll-only).

### Chain validation

`StarknetChainValidator`: `starknet_chainId []` compared case-insensitively
against the configured chain-id (hex felt, e.g. `0x534e5f4d41494e`).
Mismatch → FatalSettingError; fetch error → SettingsError. **dshackle does
not validate starknet chains at all** (it hardcodes `starknet_chainId`
responses); this is a fix, not parity.

### Health validation

`StarknetHealthValidator`: `starknet_syncing []`:
- bare `false` → Available; bare `true` → Syncing (some upstreams return a
  plain boolean instead of the sync object — dshackle hit this in the wild);
- sync object → Syncing when `highest_block_num - current_block_num` exceeds
  the chain's syncing lag (5), else Available (dshackle parity);
- fetch/parse error → Unavailable.
No peer check: juno syncs from the feeder gateway, there is no p2p peer
count to validate.

### Labels and lower bounds

- Client labels via the dshackle probe pair: `pathfinder_version` answers →
  (`pathfinder`, version); else `juno_version` → (`juno`, version);
  version strings are quoted bare strings, leading `v` stripped
  (`v0.16.4` → `0.16.4`).
- `StarknetLowerBoundDetector`: our junos are archive-grade full nodes and
  starknet clients expose no `complete_ledgers`-style range. v1 emits
  STATE/BLOCK = 1 (which dshackle hardcodes too) **but verifies it first**:
  probe `starknet_getBlockWithTxHashes [{"block_number":1}]` once — success
  → bound 1; `Block not found` (code 24) → fall back to UnknownBound and log
  (a pruned node would need binary search, follow-up if we ever run one).

### Method spec (`pkg/methods/specs/starknet.json`)

The 24 methods dshackle serves, all `cacheable: false` in v1:

| group | methods | dispatch |
|---|---|---|
| blocks/txs | starknet_getBlockWithTxHashes, starknet_getBlockWithTxs, starknet_getBlockTransactionCount, starknet_getTransactionByHash, starknet_getTransactionByBlockIdAndIndex, starknet_getTransactionReceipt, starknet_getTransactionStatus | not-null retry (dshackle NotNullQuorum parity) |
| state/calls | starknet_call, starknet_getEvents, starknet_getStateUpdate, starknet_getStorageAt, starknet_getClass, starknet_getClassHashAt, starknet_getClassAt | default |
| meta | starknet_chainId, starknet_syncing, starknet_specVersion, starknet_blockNumber, starknet_blockHashAndNumber, starknet_estimateFee, starknet_estimateMessageFee, starknet_getNonce | default (no dshackle-style hardcoding of chainId/syncing — these are our own nodes) |
| submit | starknet_addInvokeTransaction, starknet_addDeclareTransaction, starknet_addDeployAccountTransaction | not-null, single upstream — **not broadcast**: resubmitting an identical starknet tx is an *error*, not an EVM-style success echo (`DUPLICATE_TX=59` while in mempool, `INVALID_TRANSACTION_NONCE=52` after inclusion), so fan-out manufactures errors on every node but the first. This is exactly why dshackle's broadcast is broken there ("fix broadcast on starknet" TODO, ships NotNull). Parity kept; a correct broadcast treating 59 as success-equivalent is a follow-up. |

No translations, no bans, no aliases.

## Testing

1. **Unit tests** (family parity): head parsing, chain-id validation
   mainnet-vs-sepolia cross-check, syncing bool/object/lag states, label
   probe fallback chain (pathfinder → juno → none), bound-1 verification
   probe incl. the block-not-found fallback, method-spec loading.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   locally against the production sepolia node over VPN (root path). Corpus:
   every spec method node-direct vs nodecore — blocks by latest/number/hash
   (genesis and mid-history), tx by hash + receipt + status, call/getNonce/
   getStorageAt at explicit blocks, getEvents with a small filter,
   estimateFee error passthrough, future-block `Block not found` (24)
   passthrough, unknown method, invalid params. `starknet_addInvokeTransaction`
   only with a deterministically invalid transaction (expected error
   passthrough, nothing submitted). Mainnet corpus is impossible today (no
   node deployed) — chain validation for `SN_MAIN` is covered by unit tests;
   note for rollout.
3. **Staged rollout** is deployment-side work, out of scope for this repo.

## Out of scope / follow-ups

- Spec-v0.8 WebSocket subscriptions (starknet_subscribeNewHeads) if/when
  juno's WS is enabled in our deployment.
- Distinct `l1_accepted` finalized head.
- Versioned-path pinning decision at rollout time (juno root = latest spec
  and drifts; pathfinder root is operator-configurable).
- `starknet_getEvents` continuation tokens are node-local
  (`INVALID_CONTINUATION_TOKEN=33` when replayed against another upstream) —
  pagination stickiness matters once we run multi-node pools.
- Idempotent broadcast for add* (treat DUPLICATE_TX=59 as success).
- ton follows as a separate family design.
