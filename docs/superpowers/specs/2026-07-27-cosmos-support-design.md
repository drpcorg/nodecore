# Cosmos family support — design

Date: 2026-07-27
Status: implemented — see [Deltas from the original design](#deltas-from-the-original-design)
for the points where the shipped code differs from the plan below.

## Goal

Add first-class support for the **Cosmos SDK** blockchain family to nodecore
(`BlockchainType = "cosmos"`), covering all 12 `type: cosmos` protocols already
registered in `chains.yaml` (cosmos-hub, axelar, osmosis, neutron, babylon,
agoric, coreum, fetchai, provenance, initia, injective, mantra — 19 networks
including testnets).

Before this change, a `type: cosmos` upstream **panicked at startup**:
`getChainSpecific` had no `chains.Cosmos` case, and `getMethodSpecName` had no
`Cosmos` case, so those chains resolved to the empty method spec.

dshackle supports cosmos over the Tendermint JSON-RPC only
(`BlockchainType.COSMOS → ApiType.JSON_RPC`, 24 methods in
`DefaultCosmosMethods`). nodecore covers all three surfaces a Cosmos node
actually exposes.

## Scope

- **In scope:** three client-facing surfaces —
  1. Tendermint/CometBFT RPC as **JSON-RPC** (`POST /`, port 26657),
  2. the same Tendermint methods as **URI calls** (`GET /<method>?<args>`),
  3. the Cosmos SDK **LCD** REST API (port 1317).

  A new `tendermint` connector type serving (1) and (2) from one connector; a
  `cosmos-tendermint` + `cosmos-rest` + `cosmos` bundle spec set; a
  `tendermint_specific` package (reusable by any CometBFT chain) and a
  `cosmos_specific` complex specific that dispatches on the primary connector.

- **Out of scope (YAGNI):**
  - **Caching** — everything ships `cacheable: false`; see Cacheability.
  - **CometBFT `/websocket` subscriptions** — head tracking is poll-only, as
    in dshackle (which leaves its `tm.event='NewBlockHeader'` subscription
    commented out).
  - **Chain-specific LCD modules** — `osmosis/*`, `injective/*`,
    interchain-security are not declared.
  - **`type: eth` cosmos-EVM chains** (cronos, evmos, haqq, kava, zeta-chain,
    dymension, sei, okt, xrpl, tac, mezo — see `cosmos-nodes.md`). Attaching
    `tendermint` / `rest` connectors to those is a separate change with a much
    larger blast radius (it touches the EVM specific and the `eth` spec's
    connector set).

## Approach

### The `tendermint` connector: one connector, two wire shapes

CometBFT serves an identical method set as JSON-RPC on `POST /` and as URI
calls on `GET /<method>?<args>`, both answering with the same
`{"jsonrpc":..,"result":..}` envelope. So the wire shape is a property of the
*request*, not of the connector — `HttpConnector.SendRequest` forwards a
tendermint request in whichever shape it arrived:

```go
case specs.TendermintConnector:
    if request.RequestType() == protocol.JsonRpc {
        return h.sendJsonRpc(ctx, request)
    }
    return h.sendRest(ctx, request)
```

Nothing else in the connector changes: `sendRest` already builds
`GET /status` out of the `GET#/status` template, and `sendJsonRpc` already
POSTs the body verbatim to the endpoint root.

`TendermintConnector` is inserted between `JsonRpcConnector` and
`RestConnector` in the `ApiConnectorType` iota. The order is load-bearing:
`GetBestConnector(DefaultMode)` takes the minimum, and on an upstream carrying
both cosmos APIs the tendermint one must win the head/internal-request role.
The enum values are never serialized, so inserting mid-enum is safe.

### Specs: two entries per tendermint method

Rather than teaching the framework to alias one method entry to two
transports, each tendermint method is declared twice — `status` and
`GET#/status`. This needs **zero framework change**: `buildRestMatcher`
already keys REST routing off the `#` in a method name, and JSON-RPC
resolution is exact-name. Each form then carries its own name into the
connector, so no cross-mapping is needed anywhere.

Specs:
- `cosmos-tendermint` (plain, `api-connectors: ["tendermint"]`) — 28 methods ×
  2 shapes = 56 entries. That is dshackle's 24 (`DefaultCosmosMethods`), plus
  `header` / `header_by_hash` (CometBFT ≥ 0.35, which dshackle's list predates),
  plus `dump_consensus_state` / `consensus_state` declared with
  `"enabled": false` — dshackle marks the latter two "not safe", and declaring
  them disabled documents the decision while still letting an operator opt in
  via `methods.enable`. The four `broadcast_*` methods sit in a `broadcast`
  group so transaction submission can be toggled as a set.
- `cosmos-rest` (plain, `api-connectors: ["rest"]`) — 175 routes: every
  standard SDK module plus CosmWasm and IBC.

No method in either spec declares a `dispatch` policy. dshackle gives the
broadcast methods `BroadcastQuorum`, but fan-out for cosmos is deferred, so
`broadcast_*` and `POST /cosmos/tx/v1beta1/txs` route like any other method for
now.
- `cosmos` (bundle) — imports both. The two specs share no method names, and
  tendermint URI paths live at the root (`/status`, `/block`) while LCD routes
  are namespaced (`/cosmos/*`, `/cosmwasm/*`, `/ibc/*`), so there is no
  collision in the shared path matcher.

### Cacheability: nothing, deliberately

Every cosmos method is `cacheable: false`, for two independent reasons:

1. The LCD selects historical state with the **`x-cosmos-block-height`
   header**, and `calculateRestHash` deliberately excludes headers from the
   cache key — a cached balance would be served to a request asking for a
   different height.
2. CometBFT's `height` argument is **optional** (omitted means *latest*), and
   there is no tag parser to detect that, so caching `block` would pin a stale
   head. Note that `isHexNumberOrTag` accepts only `0x…` or an EVM block tag,
   so it rejects the decimal heights CometBFT and the LCD use. Only six specs
   ship a tag parser at all — `eth-json-rpc`, `arbitrum`, `fantom`, `harmony1`,
   `klaytn-json-rpc`, `tron-json-rpc` — and every one belongs to a `type: eth`
   chain. No non-EVM chain family declares one, and cosmos follows that
   precedent.

Unlocking caching later needs a decimal-height `ParserReturnType` (with a
`.height // "latest"` jq path so an omitted height reads as a block tag) and
`x-cosmos-block-height` folded into the REST cache key. Both touch shared
`pkg/methods` / `protocol` code used by every chain, so they are out of this
diff.

### Specifics

`tendermint_specific.TendermintChainSpecific` lives in its own package because
the CometBFT consensus API is shared by every Cosmos SDK chain *and* by non-SDK
CometBFT chains (BeaconKit, Heimdall), independently of whether an LCD sits
next to it. All its internal probes go out as **JSON-RPC** so the shared
`parseJsonRpcBody` path unwraps CometBFT's `result` envelope and surfaces its
errors.

`cosmos_specific.NewCosmosSpecific` is the complex specific, dispatching on the
primary connector exactly like `NewTronSpecific`: `tendermint` →
`TendermintChainSpecific`, `rest` → `CosmosRestSpecific`, anything else →
error.

Per-signal sources:

| Signal | tendermint | rest (LCD) |
| --- | --- | --- |
| Head | `block` (no height ⇒ latest) | `blocks/latest` |
| Finalized | = head (CometBFT commits are final) | = head |
| Safe | n/a — safe detection disabled | n/a |
| Syncing | `status` → `sync_info.catching_up` | `GET /…/syncing` |
| Peers | `net_info` → `n_peers` (string!) | not exposed |
| Chain validation | `status` → `node_info.network` | `default_node_info.network` |
| Lower bound | `status` → `earliest_block_height`, one call | binary search over `blocks/{height}` via the shared `LowerBoundSearchCalculator` |
| Labels | `client_type=cosmos`, CometBFT version | `client_type=cosmos`, SDK app version |

Health validators improve on dshackle, which only calls `health` and ignores
the payload: the syncing validator reads the node's own `catching_up` flag.
Both health validators are gated on `ValidateSyncing` / `ValidatePeers`, which
default to false in `default` mode and true in `strict` — relevant because
`net_info` is frequently firewalled off on hosted endpoints.

Chain validation is strict: anything other than a case-insensitive match
against the configured chain-id is a `FatalSettingError`, including an empty
`network`. An upstream that answers the probe but cannot say which chain it
serves is refused at startup rather than retried — settings validation runs
synchronously before an upstream is allowed to start.

The tendermint lower bound is reported verbatim, `0` included; only a height
that isn't a decimal number is an error. Both detectors claim `StateBound`
only — the earliest retained height says nothing about which of tx / receipts /
logs an upstream can serve.

### Hash encodings agree across connectors

CometBFT renders block hashes as uppercase hex, the LCD as base64.
`blockchain.NewHashIdFromString` tries hex first, then base64 — and a 32-byte
base64 hash is 44 chars ending in `=`, which cannot hex-decode — so both
encodings reduce to the same bytes. A tendermint-driven and an LCD-driven
upstream of the same chain therefore report identical head hashes. This is
covered by an explicit test.

### No combined-connector warning

TON logs a warning when both its connectors are on one upstream, because its
two APIs have independent data windows and failure modes. Cosmos is the
opposite: 26657 and 1317 are served by the same process over the same store, so
they share a head and a retention window. Both connectors on one upstream is
the *normal* deployment and gets no warning.

## Deltas from the original design

Decided during implementation, after the plan above was approved:

- **No `dispatch: "broadcast"` anywhere.** The plan gave the four tendermint
  `broadcast_*` methods and the LCD `POST /cosmos/tx/v1beta1/txs` a broadcast
  fan-out policy, matching dshackle. Dropped for now; the `broadcast` group
  remains so the methods can still be toggled as a set.
- **Unknown network is fatal, not retryable.** The plan had an empty
  `network` / `default_node_info.network` return `SettingsError` on the theory
  that a blank field means a failed read (a gateway's 200 error envelope)
  rather than a wrong chain. The shipped validators treat any non-match as
  `FatalSettingError`. Consequence: such an upstream is refused at startup
  instead of recovering on the next validation tick.
- **A zero lower bound is published, not rejected.** The plan errored on
  `earliest_block_height: "0"`. The shipped detector reports whatever the node
  says. Consequence: a node that reports 0 is advertised as serving from
  genesis; the codebase's usual "we don't know" signal is `UnknownBound`, which
  this path no longer emits.
- **Both bound detectors claim `StateBound` only.** The plan had them also
  claim `UnknownBound`.
- **Spec JSON is expanded, one field per line**, matching `eth-json-rpc.json`
  and `ton-http-v2.json` rather than the compact `aptos.json` style.
- **`specific_helpers` was extracted after the fact.** The plan parked the
  shared DTOs and fetch helpers in the `*_validations` packages (the aptos
  precedent); they were moved out once it was clear four different subtrees
  call them.

## Files

- `pkg/methods/specs/{cosmos-tendermint,cosmos-rest,cosmos}.json`
- `pkg/methods/data.go` — `TendermintConnector`
- `pkg/chains/chains.go` — `case Cosmos: return "cosmos"`
- `internal/upstreams/connectors/http_connector.go` — request-shape dispatch
- `internal/upstreams/upstream_factory.go` — connector + specific wiring
- `internal/upstreams/blocks/head_processor.go` — tendermint polls its head
- `internal/upstreams/chains_specific/tendermint_specific/`
- `internal/upstreams/chains_specific/cosmos_specific/`
- `internal/upstreams/chains_specific/specific_helpers/` — the shared request /
  response plumbing: LCD route templates, the wire DTOs both families
  unmarshal into, and the fetch/parse helpers around them. It has its own
  package because the specifics, validators, label detectors and lower-bound
  detectors all call it; a `validations` package should hold validators, not
  transport helpers for three other packages. RPC method names are *not* kept
  here as constants — they are passed as literals at the call site.
- `internal/upstreams/validations/{tendermint,cosmos}_validations/`
- `internal/upstreams/labels/{tendermint,cosmos}_labels/`
- `internal/upstreams/lower_bounds/{tendermint,cosmos}_bounds/`
- `internal/config/configs/upstreams/cosmos-{tendermint-and-rest,tendermint-only}.yaml`
  — fixtures for the connector-preference and head-connector tests
- tests: `pkg/methods/cosmos_spec_test.go`, `pkg/chains/cosmos_chains_test.go`,
  `internal/config/cosmos_upstream_config_test.go`,
  `internal/upstreams/connectors/tendermint_connector_test.go`, plus one test
  file per specific
- docs: `05-upstream-config.md` (connector type + Cosmos deployment),
  `11-method-specs.md` (dual-shape convention + spec tables), `README.md`
