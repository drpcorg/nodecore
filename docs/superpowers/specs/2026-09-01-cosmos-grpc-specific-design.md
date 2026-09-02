# Cosmos gRPC chain-specific — design

## Goal

Let a Cosmos upstream run on the gRPC connector alone: heads, health,
chain-id validation, client labels, and lower bounds all probed over
`cosmos.base.tendermint.v1beta1.Service`. Client traffic for the whole
`cosmos-grpc` method set (spec shipped in `drpcorg/public` v1.2.0, imported
by the `cosmos` bundle) already flows through the generic gRPC path built for
Sui — this task adds only the chain-specific layer.

## Context

- `NewCosmosSpecific` (`internal/upstreams/chains_specific/cosmos_specific/cosmos_chain_specific.go`)
  picks the flavor from the internal-request connector: `tendermint` →
  `tendermint_specific`, `rest` → `CosmosRestSpecific`. gRPC is missing.
- Connector priority (`GetBestConnector`, DefaultMode = min of the
  `ApiConnectorType` enum): `tendermint < rest < grpc`. **Unchanged.** The
  gRPC specific is chosen only when gRPC is the upstream's sole non-additional
  connector; mixed upstreams keep tendermint/rest probes and serve gRPC
  traffic alongside.
- `drpcorg/public` v1.2.0 is already bumped in `go.mod` (working tree). It
  brings `cosmossdk.io/api` transitively (typed request/response messages)
  and `pkg/cosmos` — the descriptor-registration package for ingress
  reflection (blank import is enough).

## Ruling: a new pure `cosmosGrpcSpecific`

A standalone type, sui-style — not a generalization of `CosmosRestSpecific`
and not connector-branching inside the shared REST helpers. The gRPC probe
stack is its own parallel set of helpers/validators/detectors; only
transport-agnostic logic (pruned-message hints, the lower-bound search
calculator, `CosmosClient` label constant) is shared.

## Probes

All on `cosmos.base.tendermint.v1beta1.Service`, all present in
`cosmos-grpc.json`:

| concern | gRPC method | notes |
|---|---|---|
| head | `GetLatestBlock` | real hashes: `block_id.hash` bytes → hex; parent from `block.header.last_block_id` |
| finalized | = head | committed cosmos block is final (same as rest/tendermint) |
| health (syncing) | `GetSyncing` | gated on `options.ValidateSyncing` |
| chain-id validation | `GetNodeInfo` | compare `default_node_info.network`, case-insensitive; fatal on mismatch |
| client labels | `GetNodeInfo` | `application_version.version`, fallback `default_node_info.version`; client type `CosmosClient` |
| lower bound | `GetBlockByHeight` | StateBound only, same as rest |

No head subscription — the spec has no streaming service →
`ErrUnsupportedHeadSubscriptions` from both `SubscribeHeadRequest` and
`ParseSubscriptionBlock`. `CapDetectors` and `MethodsProcessor` return nil
(reflection-based method detection stays parked, as for Sui).

Probes cross the schema boundary as bytes (gRPC-v1 principle):
`proto.Marshal` into `protocol.NewInternalUpstreamGrpcRequest`,
`proto.Unmarshal` on `ResponseResult()`. Typed messages come from
`cosmossdk.io/api/cosmos/base/tendermint/v1beta1`.

## Components

- `internal/upstreams/chains_specific/specific_helpers/cosmos_grpc.go` — the
  gRPC twin of `cosmos.go`: method-name constants, request builders,
  fetch+parse helpers (`FetchCosmosGrpcNodeInfo`, `FetchCosmosGrpcLatestBlock`,
  `FetchCosmosGrpcSyncing`, block-by-height request), mirroring `sui.go`'s
  shape.
- `internal/upstreams/chains_specific/cosmos_specific/cosmos_grpc_specific.go`
  — `cosmosGrpcSpecific` implementing `chains_specific.ChainSpecific`;
  constructor rejects any non-gRPC connector; wired as the
  `specs.GrpcConnector` case in `NewCosmosSpecific`.
- gRPC flavors of the probe pieces, next to the existing ones:
  `cosmos_validations` (chain + syncing validators), `cosmos_labels`
  (client-labels detector), `cosmos_bounds` (lower-bound detector sharing
  `isPrunedMessage`/`prunedHints` and `LowerBoundSearchCalculator`).
- Ingress reflection: blank-import `github.com/drpcorg/public/pkg/cosmos`
  where the Sui descriptors are linked today, so reflection serves the cosmos
  services. Reflection listing stays global across chains (settled earlier —
  cosmetic, never chain-scoped).

## Error handling: pruned vs outage over gRPC

No HTTP codes exist on this path. The connector maps upstream errors through
`status.FromError` into a typed `protocol.GrpcStatus{Code, Message}`
(`GrpcStatusFromError`). The lower-bound probe treats as **pruned**:

- canonical codes `InvalidArgument`, `NotFound`, `OutOfRange`, **or**
- a `prunedHints` match on the status message ("lowest height is", …).

Everything else (`Unavailable`, `DeadlineExceeded`, `Internal`, transport
failures) is returned as an error so the search calculator retries instead of
mistaking an outage for pruning.

## Testing

- `cosmos_grpc_specific_test.go` mirroring `cosmos_chain_specific_test.go`:
  mock connector returning proto-marshalled responses; cover head parsing
  (hash/parent linkage, zero/invalid height), syncing verdicts, chain-id
  match/mismatch/fatal, label extraction with version fallback, lower-bound
  pruned-vs-outage per code and per message hint, and the constructor's
  connector-type rejection.
- Helper-level tests in `specific_helpers` where `sui.go`'s tests set the
  precedent.

## Out of scope

- Head streaming for cosmos (no upstream service exists).
- Reflection-based `MethodsProcessor`.
- Any connector-priority or config changes — gRPC connectors already validate
  (onion endpoints rejected) and the `cosmos` bundle already declares `grpc`.
