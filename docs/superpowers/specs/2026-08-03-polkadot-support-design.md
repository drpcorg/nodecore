# Polkadot support

Port dshackle's `PolkadotChainSpecific` to nodecore, giving the `polkadot`
blockchain type a chain-specific object, method specs, health/settings
validators and a state lower bound.

Source of truth for parity:
`dshackle/src/main/kotlin/io/emeraldpay/dshackle/upstream/polkadot/` and
`upstream/calls/DefaultPolkadotMethods.kt`.

## Scope

`chains.Polkadot` already exists in `pkg/chains/chains.go`, and 11 protocol
groups in the `pkg/chains/public` submodule are `type: polkadot`:

| protocol | chains | chain-id |
| --- | --- | --- |
| polkadot | Mainnet | `Polkadot` |
| kusama | Mainnet | `Kusama` |
| vara | Mainnet, Testnet | `Vara Network` |
| avail | Mainnet, Testnet | `Avail Network` |
| polymesh | Mainnet, Testnet | `Polymesh Mainnet` / `Polymesh Testnet` |
| westend | Mainnet | `Westend` |
| westend-asset-hub | Mainnet | `Westend Asset Hub` |
| paseo | Mainnet | `Paseo` |
| paseo-asset-hub | Mainnet | `Paseo Asset Hub` |
| polkadot-asset-hub | Mainnet | `Polkadot Asset Hub` |
| zkverify | Mainnet, Testnet | `ZkVerify Mainnet` / `ZkVerify Testnet` |

The `chain-id` column is what `PolkadotChainValidator` compares `system_chain`
against, so a node reporting anything else is a fatal settings error. **Two of
these values are wrong in the submodule**, confirmed by probing live nodes: avail
reports `Avail DA Mainnet` (not `Avail Network`, cross-checked on two providers)
and zkverify reports `zkVerify` (not `ZkVerify Mainnet`). Both need fixing in
`drpcorg/public` or every upstream on those chains is refused at startup. The
seven confirmed correct are polkadot, kusama, westend, vara, polymesh and the
polkadot/westend asset hubs; paseo, paseo-asset-hub and the testnets remain
unverified (no reachable public endpoint at the time of writing).

Today `getChainSpecific` panics with `unknown blockchain type - polkadot` for all
of them. Adding the factory case plus the `getMethodSpecName` mapping enables
every one of them at once - there is no per-chain Go work. Avail is the only
chain needing anything extra, and only for its 11 extra methods (Component 4).

Chain settings are not uniform across the family, which matters for the health
validator: `polkadot`, `kusama`, `vara` and `avail` ship
`expected-block-time: 3s` and `options.validate-peers: false`, while the other
seven ship `expected-block-time: 6s` and pin no peers option at all - so the
peers arm follows the mode default there and **is on in strict mode**. All 11
ship `lags: {syncing: 10, lagging: 5}`.

## Decisions

These were settled during design and constrain everything below.

1. **Block hash: dshackle parity via a second RPC call.** A Polkadot header does
   not contain its own hash, so the hash comes from a follow-up
   `chain_getBlockHash(number)`. Rejected: synthesizing a hash from the height
   (the aptos/solana pattern - kills any future fork-choice), and computing
   blake2-256 over the SCALE-encoded header locally (exact for Polkadot, but
   Avail's header carries extra fields so its encoding differs and the hash would
   be silently wrong).
2. **Nothing is cacheable.** dshackle caches nothing for polkadot, and nodecore
   cannot express what would be needed: Polkadot state reads take an *optional*
   block hash, so `chain_getHeader []` (means "latest") and
   `chain_getHeader ["0x..."]` (immutable) are the same spec entry.
   `isMethodCacheable` sees `ParseParams` return nil for the former and caches
   it. Every method therefore gets `"cacheable": false`.
3. **A shared `polkadot` method spec, plus a separate `avail` spec** for Avail's
   11 chain-specific methods (`kate_*`, `mmr_*`, `chainSpec_v1_*`), rather than
   pooling everything into one spec. Vara's `gear_*` stays in the shared spec,
   matching dshackle, which special-cases only Avail. The cost is an ordering
   dependency: the `avail` spec is inert until `method-spec: "avail"` lands in the
   `drpcorg/public` submodule (see Component 4).
4. **Strict dshackle parity - no finalized head, no labels, state bound only.**
   Deliberately *not* in this change, despite being cheap: a GRANDPA finalized
   head (`chain_getFinalizedHead` + `chain_getHeader`), a client label detector
   off `system_name`/`system_version`, and a block lower bound for
   `--blocks-pruning` nodes.
5. **No `_unstable_` methods, and no new method families.** dshackle's list is
   taken as-is minus anything containing `_unstable_`; the modern `chainHead_v1_*`
   / `transactionWatch_v1_*` replacements are not added. `chainSpec_v1_*` is the
   one `v1` family that stays, because dshackle already whitelists it for Avail -
   removing it would be a regression against dshackle rather than declining to
   add something new.

## Component 0: `specific_helpers/polkadot.go`

The header fetch and the height-to-hash lookup are needed by both the
chain-specific object (Component 1) and the lower-bound detector (Component 3).
`polkadot_bounds` cannot import `polkadot_specific` - that is the wrong direction
and would cycle once the chain-specific object constructs the detector - so the
shared pieces live in `specific_helpers`, exactly as `specific_helpers/cosmos.go`
serves both `cosmos_specific` and `cosmos_bounds`:

- `PolkadotHeader{ParentHash, Number string}` - the header fields nodecore uses.
- `PolkadotHeaderRequest` / `FetchPolkadotHeader` / `ParsePolkadotHeader` -
  `chain_getHeader` with no params (the best block), and its parsing. A header
  with no `number` is an error: without a height there is no block and no
  `chain_getBlockHash` argument either.
- `ParsePolkadotHeight` - substrate reports `number` as a hex string
  (`"0x1a2b3c"`); the `0x` prefix is tolerated as optional.
- `FetchPolkadotBlockHash` - `chain_getBlockHash` with the header's `number`
  passed through **verbatim**, so the node sees the representation it emitted and
  no decimal-vs-hex ambiguity arises.

## Component 1: `polkadot_specific.PolkadotChainSpecificObject`

New package `internal/upstreams/chains_specific/polkadot_specific`, modeled on
`starknet_specific` (poll-driven JSON-RPC chain, hash-bearing blocks).
Constructor takes `(ctx, configuredChain, upstreamId, connector, pollInterval,
options)` and is wired from `getChainSpecific` under `case chains.Polkadot:`.

### Head

```
GetLatestBlock(ctx):
    chain_getHeader []       -> {number: "0x...", parentHash: "0x..."}
    chain_getBlockHash [num] -> "0x..."
    => Block{Height: number, Hash: hash, ParentHash: parentHash}
```

dshackle polls `chain_getBlock`, which returns the full block including every
extrinsic. `chain_getHeader` carries the same three fields we need at a fraction
of the payload, so we use it instead.

`ParseBlock(headerJSON)` parses `number` (hex) and `parentHash` into a `Block`
and leaves `Hash` empty; both callers fill `Hash` in after the second call.
`protocol.Block` is a plain mutable struct, so this needs no new constructor.
A missing or unparseable `number` is an error.

`SubscribeHeadRequest` returns `chain_subscribeNewHeads`. `ParseSubscriptionBlock`
runs `ParseBlock` on the notification payload and then makes the same
`chain_getBlockHash` call - `solana_specific` already calls the connector from
this hook, so the pattern is established.

**Hash-call failure inside `ParseSubscriptionBlock` must not return an error.**
`blocks/head.go:189` treats a parse error as terminal and returns from the
subscription goroutine; the head then stalls until `headNoUpdatesTimeout` fires
and resubscribes. So on failure we log a warning and return the height +
parentHash block with an empty `Hash`.

This is safe for *routing*: the chain-level head merge in `chain_supervisor.go` /
`chain_supervisor_state.go` never reads `Block.Hash`, selection is height-based.
It is not invisible, though - `emerald.HeadToApi` publishes `head.Hash.ToHex()`
as the `BlockId` of every gRPC head event, so the degraded path emits an empty
`BlockId` to gRPC consumers. An empty id is the acceptable failure here: it does
not trip the self-parent guard in `HeadToApi`, and it is strictly better than the
alternative the null-guard exists to prevent - a `null` hash rendering as the
string `"null"` and base64-decoding to the bogus 3-byte id `0x9ee965`, which
would look like a real block to a consumer walking the parent chain.

`GetFinalizedBlock` returns `errUnsupportedFinalizedBlock` (decision 4), and
`BlockProcessor()` returns `nil` so it is never called - `createBlockProcessor`
in `upstream_factory.go` handles a nil processor. `LabelsProcessor()` returns
`nil` likewise (handled by `createLabelsProcessor`).

### Capabilities

`CapDetectors` returns `caps.DefaultCapDetectors(upstreamId, input.WsConnector)`.
This is required rather than optional: `WsCap` gates whether an upstream is
eligible to serve subscriptions at all (`flow/matchers.go:132`,
`chain_supervisor.go:341`), and Polkadot has 11 subscription pairs. Plain ws
presence, not the liveness-gated detector EVM uses - gating `WsCap` on head
liveness would be an improvement beyond parity and is left out.

## Component 2: `polkadot_validations`

New package `internal/upstreams/validations/polkadot_validations`.

**`PolkadotHealthValidator`** - one `system_health` call covering both arms:

```
{"peers": 42, "isSyncing": false, "shouldHavePeers": true}

isSyncing                                  => protocol.Syncing
shouldHavePeers && peers < options.MinPeers => protocol.Immature
otherwise                                   => protocol.Available
fetch/parse failure                          => protocol.Unavailable
```

The syncing arm is active when `options.ValidateSyncing`, the peers arm when
`options.ValidatePeers`. If both are off, `HealthValidators()` returns an empty
slice so no `system_health` traffic is generated at all.

This is one validator rather than the two that `tendermint_validations` uses,
because both signals arrive in a single `system_health` response - splitting
would double the probe traffic for nothing. Note that with
`validate-peers: false` in `chains.yaml` for all 11 chains, only the syncing arm
runs in practice, and only in strict mode (`ValidateSyncing` defaults to
`strict`).

**`PolkadotChainValidator`** - `system_chain` returns a plain JSON string
(`"Polkadot"`, `"Kusama"`, `"Vara Network"`, ...). Compare lowercased against
`configuredChain.ChainId` (already lowercased at load); mismatch is a fatal
settings error, matching every other chain validator. Skipped entirely when
`ChainId` is empty or `DisableChainValidation` is set.

## Component 3: `polkadot_bounds`

New package `internal/upstreams/lower_bounds/polkadot_bounds`, one detector for
`protocol.StateBound`, built on `lower_bounds.LowerBoundSearchCalculator`
(`cosmos_bounds` is the closest template). Period 5 minutes, mirroring
dshackle's `period() = 5`.

- `fetchLatestHeight`: `specific_helpers.FetchPolkadotHeader` +
  `ParsePolkadotHeight` (Component 0). Only the height matters here, so no hash
  lookup is needed.
- `probe(height)`: `chain_getBlockHash [hexHeight]` -> hash, then
  `state_getMetadata [hash]`. A non-null metadata result means the state is
  retained.
- Definite no-data answers return `(false, nil)` so the search narrows at once:
  an error message containing `State already discarded for` (dshackle's
  `nonRetryableErrors`), a `null` block hash, or a `null` metadata result. A node
  that cannot name a block at a height does not hold it - that is absence, not a
  transient fault.
- Any other error propagates, which buys retries.

**The retry path is not a guarantee, and earlier drafts of this document claimed
it was.** `LowerBoundSearchCalculator` collapses a post-retry error to no-data
(`return err == nil && available`, `lower_bound_search.go`), so a *persistent*
non-pruning failure - a `state_getMetadata` restricted by the provider, rate
limiting - still reads as pruned and pushes the bound upward, after burning up to
30 attempts with backoff to 1 minute on each of ~25 probes. This is shared
behavior across every search-based detector (cosmos included), not something this
chain introduces, which is why it is documented rather than worked around here.
Classifying null answers as no-data above removes the most likely way to hit it.
A pre-flight check that aborts detection on a `-32601` from `state_getMetadata`
would remove the rest, and belongs in the shared calculator.

Polkadot state methods key off the block *hash*, never the number, which is why
the probe needs two calls per step.

No block bound (decision 4).

## Component 4: method specs

Four files: a shared `polkadot` bundle for the 10 non-Avail chains, and a
separate `avail` spec that layers Avail's 11 extra methods on top (decision 3).

```
polkadot.json          bundle, imports polkadot-json-rpc + polkadot-websocket
polkadot-json-rpc.json plain ["json-rpc", "websocket"]  81 methods
polkadot-websocket.json plain ["websocket"]             11 subscribe methods
avail.json             plain ["json-rpc", "websocket"], imports polkadot,
                       + 11 Avail methods inline
```

`polkadot-json-rpc` declares **both** `json-rpc` and `websocket`, matching
`eth-json-rpc` and `solana-json-rpc`. Substrate serves every RPC method over ws
as well as http, so a ws-only Polkadot upstream must be credited with the regular
calls and not just the 11 subscriptions - declaring `["json-rpc"]` alone would
silently strip them (`GetSpecMethodsByConnectors` buckets methods per connector
type).

`avail.json` is a *plain* spec that imports the `polkadot` bundle and adds its own
methods, which is `hyperliquid-eth.json`'s shape ("chain family plus a few extra
methods") rather than a third bundle. This works because
`validateImportedSpecCompatibility` (`resolved_specs.go:137`) requires a plain
spec's connector set to match its import's effective set exactly - `["json-rpc",
"websocket"]` on both sides here - and Avail's methods then land on both
connectors, which is correct for substrate. The alternative shape (an `avail`
bundle importing `polkadot-json-rpc` + a separate `avail-json-rpc` +
`polkadot-websocket`, the `cosmos.json`/`tron.json` style) also validates, since
same-level imports only conflict on duplicate *method names*; it just costs an
extra file for no gain.

Every method in every file is `"cacheable": false` (decision 2), carries no
`tag-parser` (pointless once nothing is cacheable, and Polkadot's block refs are
hashes that nodecore's finalization checks cannot use anyway), and no method sets
a `dispatch` policy - including `author_submitExtrinsic`, where dshackle uses
`BroadcastQuorum`. One field per line per the repo's spec formatting.

### `polkadot-json-rpc`: 81 methods

dshackle's `all` (95) + `add` (1) minus the 15 `_unstable_` entries. Vara's
`gear_*` stays here rather than moving to a per-chain spec, matching dshackle,
which special-cases only Avail:

```
account_nextIndex, author_hasKey, author_hasSessionKeys, author_insertKey,
author_pendingExtrinsics, author_removeExtrinsic, author_rotateKeys,
author_submitExtrinsic, babe_epochAuthorship, chain_getBlock,
chain_getBlockHash, chain_getFinalisedHead, chain_getFinalizedHead,
chain_getHead, chain_getHeader, chain_getRuntimeVersion, childstate_getKeys,
childstate_getKeysPaged, childstate_getKeysPagedAt, childstate_getStorage,
childstate_getStorageEntries, childstate_getStorageHash,
childstate_getStorageSize, dev_getBlockStats, gear_calculateHandleGas,
gear_calculateInitCreateGas, gear_calculateInitUploadGas,
gear_calculateReplyGas, gear_readMetahash, gear_readState,
gear_readStateBatch, gear_readStateUsingWasm, gear_readStateUsingWasmBatch,
grandpa_proveFinality, grandpa_roundState, offchain_localStorageGet,
offchain_localStorageSet, payment_queryFeeDetails, payment_queryInfo,
runtime_wasmBlobVersion, stakingRewards_inflationInfo, state_call,
state_callAt, state_getChildReadProof, state_getKeys, state_getKeysPaged,
state_getKeysPagedAt, state_getMetadata, state_getPairs, state_getReadProof,
state_getRuntimeVersion, state_getStorage, state_getStorageAt,
state_getStorageHash, state_getStorageHashAt, state_getStorageSize,
state_getStorageSizeAt, state_queryStorage, state_queryStorageAt,
state_traceBlock, state_trieMigrationStatus, sync_state_genSyncSpec,
system_accountNextIndex, system_addLogFilter, system_addReservedPeer,
system_chain, system_chainType, system_dryRun, system_dryRunAt,
system_health, system_localListenAddresses, system_localPeerId, system_name,
system_nodeRoles, system_peers, system_properties,
system_removeReservedPeer, system_reservedPeers, system_resetLogFilter,
system_syncState, system_version
```

Dropped as `_unstable_`: the 11 `chainHead_unstable_*`, the 3
`chainSpec_unstable_*`, and `system_unstable_networkState`.

### `avail`: 11 additional methods

dshackle's `availMethods`, verbatim (none are `_unstable_`), giving
`avail`/`avail-testnet` 92 JSON-RPC methods against the other chains' 81:

```
chainSpec_v1_chainName, chainSpec_v1_genesisHash, chainSpec_v1_properties,
kate_blockLength, kate_queryDataProof, kate_queryProof, kate_queryRows,
mmr_generateProof, mmr_root, mmr_verifyProof, mmr_verifyProofStateless
```

**This spec is inert until the submodule is updated.** `getMethodSpecName` maps
the `polkadot` blockchain type to the `polkadot` spec, and a per-chain override
comes only from `method-spec:` in `pkg/chains/public/chains.yaml` - a git
submodule pointing at `drpcorg/public`. So enabling it is a two-repo change:

1. PR to `drpcorg/public` adding `method-spec: "avail"` to the `avail` protocol
   block.
2. Bump the submodule here, then `make generate-networks`.

Until step 1 lands, `avail.json` loads but nothing resolves to it, and Avail
upstreams keep using the 81-method `polkadot` spec - i.e. exactly today's
behavior minus nothing. The Polkadot work does not depend on this ordering; only
the Avail extras do.

`pkg/methods/specs/polkadot-websocket.json` - `api-connectors: ["websocket"]`,
11 subscribe methods.

Both names matter and neither is derivable from dshackle, which proxies node
frames and therefore never models notification names (`chain_newHead` appears
nowhere in the Kotlin). In nodecore, `Subscription.Method` is the notification
method stamped into the frame synthesized for the *client*
(`flow/sub_processor.go:64` -> `protocol/response.go:87`), and `UnsubMethod` is
what nodecore sends *upstream* to tear the subscription down
(`ws/ws_protocol.go:71`). Notification names are also not computable from the
subscribe name: `subscribe_newHead` / `chain_subscribeNewHead` /
`chain_subscribeNewHeads` are three aliases of one subscription that all emit
the singular `chain_newHead`, and `chain_subscribeRuntimeVersion` emits
`state_runtimeVersion` - a different prefix.

The table below is taken from drpc's existing subscription map, which is
authoritative (it is what production already speaks to Polkadot clients):

| subscribe | notification | unsubscribe |
| --- | --- | --- |
| `subscribe_newHead` | `chain_newHead` | `unsubscribe_newHead` |
| `chain_subscribeNewHead` | `chain_newHead` | `chain_unsubscribeNewHead` |
| `chain_subscribeNewHeads` | `chain_newHead` | `chain_unsubscribeNewHeads` |
| `chain_subscribeAllHeads` | `chain_allHead` | `chain_unsubscribeAllHeads` |
| `chain_subscribeFinalizedHeads` | `chain_finalizedHead` | `chain_unsubscribeFinalizedHeads` |
| `chain_subscribeFinalisedHeads` | `chain_finalizedHead` | `chain_unsubscribeFinalisedHeads` |
| `chain_subscribeRuntimeVersion` | `state_runtimeVersion` | `chain_unsubscribeRuntimeVersion` |
| `state_subscribeRuntimeVersion` | `state_runtimeVersion` | `state_unsubscribeRuntimeVersion` |
| `state_subscribeStorage` | `state_storage` | `state_unsubscribeStorage` |
| `author_submitAndWatchExtrinsic` | `author_watchExtrinsic` | `author_unwatchExtrinsic` |
| `grandpa_subscribeJustifications` | `grandpa_justifications` | `grandpa_unsubscribeJustifications` |

Dropped as `_unstable_`: `transaction_unstable_submitAndWatch` /
`transaction_unstable_watch` / `transaction_unstable_unwatch` (decision 5).

These 11 rows are pinned by assertions in `pkg/methods/polkadot_spec_test.go`.
A wrong notification name would fail silently otherwise: it cannot affect
routing (upstream notifications are matched purely on `params.subscription` -
`protocol/parse_response.go:230` never reads the incoming `method`) and cannot
affect the payload, so nodecore emits a well-formed frame that only the client
library rejects, dropping events with no error on our side.

No Go work is needed for subscription routing: `resolveSource` in
`flow/sub_aggregation.go` falls through to `newGenericSourceBuilder` for any
subscribe method that is not an EVM special case, so all 11 aggregate and
dedupe by `(method, params)` for free.

## Component 5: wiring

In this repo:

- `internal/upstreams/upstream_factory.go` - `case chains.Polkadot:` in
  `getChainSpecific`, returning the new object.
- `pkg/chains/chains.go` - `case Polkadot: return "polkadot"` in
  `getMethodSpecName`.

In `drpcorg/public`, as a follow-up that this PR does not block on:

- `method-spec: "avail"` on the `avail` protocol block, then a submodule bump
  here. Only the Avail extras depend on it.

## Not ported

**`BasicPolkadotUpstreamRpcMethodsDetector`.** dshackle probes `rpc_methods` at
runtime to learn which methods an upstream supports. nodecore has no equivalent
mechanism - the supported set comes from the method spec plus per-upstream
`methods.enable`/`disable` config (`internal/upstreams/methods/methods.go`), and
inventing a runtime detector for one chain family is out of scope.

**dshackle's `chainHead_unstable_follow` classification.** dshackle lists it as a
plain method even though it is a subscription. Moot here - all `_unstable_`
methods are dropped (decision 5).

## Testing

Table-driven unit tests alongside each new file, following the existing
chain-specific test suites (`starknet_chain_specific_test.go`,
`tendermint_chain_specific_test.go`) and using `pkg/test_utils` connector mocks.

- `specific_helpers/polkadot_test.go` - header parsing rejects a missing/garbage
  `number`; `ParsePolkadotHeight` handles prefixed and unprefixed hex; the
  block-hash call passes `number` through verbatim and rejects an empty result.
- `polkadot_chain_specific_test.go` - `GetLatestBlock` issues both calls and
  assembles height/hash/parentHash; `ParseBlock` rejects a missing/garbage
  `number`; `ParseSubscriptionBlock` returns a hash on success and degrades to an
  empty hash (no error) when `chain_getBlockHash` fails; `GetFinalizedBlock`
  errors; `BlockProcessor`/`LabelsProcessor` are nil; `HealthValidators` is empty
  when both flags are off.
- `polkadot_validators_test.go` - syncing, insufficient peers,
  `shouldHavePeers: false` with zero peers (must stay `Available`), healthy,
  malformed response; chain validator match / mismatch / empty `ChainId`.
- `polkadot_lower_bound_test.go` - converges on a pruned node, treats
  `State already discarded for` as pruned, propagates other errors.
- `pkg/methods/polkadot_spec_test.go` (following `cosmos_spec_test.go`):
  - Resolved method counts, which is what catches a botched import or a
    connector-set mismatch - both fail as a silently smaller method set rather
    than an error. Because `polkadot-json-rpc` covers *both* connectors, the
    per-connector totals differ:

    | spec | `json-rpc` | `websocket` |
    | --- | --- | --- |
    | `polkadot` | 81 | 92 (81 + 11 subs) |
    | `avail` | 92 (81 + 11 avail) | 103 |

  - `avail` is a strict superset of `polkadot` on both connectors.
  - The regular calls appear on `websocket` too, so a ws-only upstream is not
    stripped down to subscriptions.
  - No method in either spec is cacheable, and none sets a dispatch policy.
  - Every subscribe entry has the exact notification and unsubscribe names from
    the table above, asserted row by row - these are the values clients dispatch
    on, and nothing else in the system would catch a typo.

## Docs

- `docs/nodecore/05-upstream-config.md` - add polkadot to the chain-family
  table. No per-chain deployment section.
- `docs/nodecore/11-method-specs.md` - add `polkadot` -> `polkadot-json-rpc`,
  `polkadot-websocket` and `avail` -> `polkadot` + inline to the bundle table
  (~line 224), and the new specs to the per-connector table (~line 235). Also a
  short note on why nothing is cacheable, mirroring the cosmos paragraph at
  ~line 184: Polkadot's block ref is an *optional* hash argument, so an omitted
  ref means latest and no tag parser detects it.
- `docs/nodecore/04-cache.md` already lists `polkadot` as a valid
  `blockchain-types` value; no change needed, though nothing is cacheable.
- `README.md` - supported-chains mention, matching how cosmos and TON were added.
