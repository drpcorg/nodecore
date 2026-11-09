# Tron gRPC: `grpc-additional` connector and the Tron gRPC chain-specific — design

Date: 2026-10-07
Status: approved 2026-10-07, implemented (working tree); probes switched to `GetBlock(detail: false)` after review

## Goal

Route the java-tron gRPC API through nodecore the way the java-tron HTTP API is
routed today, and let a Tron upstream run on gRPC alone:

1. teach nodecore the new `grpc-additional` api connector shipped by
   `drpcorg/public` PR #281 (the gRPC twin of `rest-additional`), so an upstream can
   carry a second gRPC endpoint for the solidity port;
2. add a `TronGrpcSpecific` that probes heads, finality, health, client labels and
   lower bounds over `protocol.Wallet`, mirroring the REST specific probe for probe.

Client traffic for the three new specs (`tron-grpc`, `tron-grpc-solidity`,
`tron-grpc-database`) already flows through the generic gRPC path built for Sui and
Cosmos once the connector type is known. The gRPC ingress serves reflection for the
new services once the descriptors are linked.

## Context

- `drpcorg/public` PR #281 (branch head `6f48f45`, "Tron grpc") adds:
  - `specs.GrpcAdditional` (`"grpc-additional"`), listed in
    `additionalApiConnectors`, and `specs.IsGrpcApiConnectorType` covering
    `GrpcConnector` and `GrpcAdditional`;
  - `tron-grpc` (`protocol.Wallet`, 147 methods, connector `grpc`),
    `tron-grpc-solidity` (`protocol.WalletSolidity`, 47 methods, connector
    `grpc-additional`), `tron-grpc-database` (`protocol.Database`, 4 methods,
    connectors `grpc` then `grpc-additional`), all imported by the `tron` bundle.
    Everything unary, nothing cacheable, no `grpc` settings block;
  - generated descriptors `pkg/tron/api` + `pkg/tron/core`;
  - `GetGrpcServices` advertises services from both gRPC connectors.
  - `chains.yaml` is untouched, so `make generate-networks` is not needed.
- java-tron serves `protocol.Wallet` on the full-node gRPC port (50051) and
  `protocol.WalletSolidity` on the solidity port (50061). gRPC has no URL path to tell
  them apart, so they are two connectors, exactly like `rest` (`/wallet/*`) and
  `rest-additional` (`/walletsolidity/*`).
- The HTTP API is a JSON rendering of the same protobuf messages. Every REST probe in
  `internal/upstreams/chains_specific/tron_specific/tron_rest_specific.go` and its
  validators/detectors has a one-to-one gRPC twin on `protocol.Wallet`.
- nodecore already treats additional connectors generically: `GetBestConnector`
  filters them out, `validateHeadConnector` rejects them, `validate` rejects an
  additional-only upstream, and `getMethodConnector` walks the spec's connector
  order per method. None of that changes.
- Connector priority (`GetBestConnector`, DefaultMode = min of the enum):
  `json-rpc < rest < grpc`. **Unchanged.** The gRPC specific is chosen only when
  `grpc` is the upstream's sole plain connector; mixed upstreams keep the json-rpc or
  REST probes and serve gRPC traffic alongside. In `strict` mode the ranking inverts
  and a mixed upstream probes over gRPC (same ruling as cosmos).

## Rulings

| Question | Decision |
|---|---|
| Dependency pin | `github.com/drpcorg/public` v1.4.8 (PR #281 released). |
| Second gRPC port | New connector type `grpc-additional`, reusing `GrpcConnector` with a type parameter (the `HttpConnector` pattern). No second struct. |
| Which service the probes use | `protocol.Wallet` only. `protocol.Database` also serves `GetNowBlock`/`GetBlockByNum`, but the probes run on the primary `grpc` connector where Wallet is always present. |
| Specific shape | A standalone `TronGrpcSpecific` next to `TronRestSpecific`, sui/cosmos style. No connector branching inside the REST probes. |
| `has_grpc` auto label | Fires for any gRPC-family connector (`specs.IsGrpcApiConnectorType`), so a `rest + grpc-additional` upstream advertises it too: it does serve gRPC methods. |
| Shared parsing | The `solidityBlock` string parser and the syncing drift arithmetic are extracted into helpers used by both the REST and the gRPC probes. Nothing else is shared. |
| Docs | A `grpc-additional` bullet in the connectors section of `docs/nodecore/05-upstream-config.md`. No per-chain deployment section. |

## Task 1 — `grpc-additional` plumbing

Shippable alone: after it, `tron-grpc-solidity` methods route on any upstream that
configures a `grpc-additional` connector next to its plain ones, and reflection lists
the three Tron services.

### Changes

- `go.mod` / `go.sum`: pin `github.com/drpcorg/public` to v1.4.8.
- `internal/upstreams/connectors/grpc_connector.go`: `NewGrpcConnector` and
  `NewGrpcConnectorWithClientConn` take a `connectorType specs.ApiConnectorType`;
  `GetType` returns it. Today it is hardcoded to `GrpcConnector`, and
  `GenericUpstream.GetConnector` keys connectors by `GetType`, so two gRPC
  connectors on one upstream would collide without this.
- `internal/upstreams/upstream_factory.go`: `case specs.GrpcAdditional` →
  `connectors.NewGrpcConnector(connectorConfig, specs.GrpcAdditional, upId)`; the
  existing `GrpcConnector` case passes `specs.GrpcConnector`.
- Four `== specs.GrpcConnector` checks become `specs.IsGrpcApiConnectorType`:
  - `internal/protocol/request.go` `CanBeServedBy`: a `Grpc` request needs any
    gRPC-family connector; `JsonRpc`/`Ws`/`Rest` need any non-gRPC connector;
  - `internal/server/grpc_ingress/chain_ingress.go` method gate ("only gRPC methods
    are served here");
  - `internal/server/emerald/native_call_adapter_grpc.go` method gate;
  - `internal/config/upstream_config.go` `.onion` rejection (the gRPC connector
    dials directly for both types).
- `internal/config/defaults.go` `setHasGrpcLabel`: `specs.IsGrpcApiConnectorType`.
- `internal/server/grpc_ingress/chain_descriptors.go`: blank-import
  `github.com/drpcorg/public/pkg/tron/api`. `TestChainDescriptorsCoverSpecServices`
  and `TestChainDescriptorsResolveEveryImportByFilename` then cover
  `protocol.Wallet`, `protocol.WalletSolidity`, `protocol.Database` with no new
  test code.
- `docs/nodecore/05-upstream-config.md`: add `grpc-additional` to the connector
  type lists (the `connectors` bullet, the `type` enum, the head-connector
  exclusion) and a bullet under Connectors: a second gRPC endpoint of the same
  upstream, java-tron solidity port as the example, same `tls`/`ca` settings as
  `grpc`, cannot be the only connector, never the head connector.

### Untouched on purpose

- `head_processor.go` connector switch: an additional connector is never the head
  connector, so no `GrpcAdditional` case.
- `httpConnectorTypes`: `grpc-additional` is not HTTP-backed.
- Spec-vs-config connector validation, method resolution per upstream
  (`GetSpecMethodsByConnectors`), strategy, metrics: all keyed on the connector
  type string or the spec data and pick the new type up unchanged.

### Tests

- `grpc_connector_test.go`: `GetType` reflects the constructor argument for both
  types.
- `upstream_factory` test: a `grpc-additional` config yields a connector whose type
  is `GrpcAdditional`.
- Config tests: a `tron` upstream with `grpc` + `grpc-additional` validates; a
  `grpc-additional`-only upstream is rejected by the existing additional-only rule;
  `grpc-additional` as `head-connector` is rejected; `has_grpc` is set for
  `rest + grpc-additional`; an `.onion` url is rejected for `grpc-additional`.
- `protocol/request_test.go`: `Grpc.CanBeServedBy([grpc-additional])` is true,
  `JsonRpc.CanBeServedBy([grpc, grpc-additional])` is false.
- Ingress test: a `/protocol.WalletSolidity/...` request passes the method gate on
  the `tron` spec.

## Task 2 — `TronGrpcSpecific`

### Selection

`NewTronSpecific` gains `case specs.GrpcConnector: return newTronGrpcSpecific(...)`.
The constructor rejects a nil connector and any connector whose type is not
`GrpcConnector` (the additional type never reaches a specific).

### Probes, one for one with the REST specific

All requests are built with `protocol.NewInternalUpstreamGrpcRequest` on
`chain.Chain`, bodies are proto-marshalled messages from
`github.com/drpcorg/public/pkg/tron/api` (`EmptyMessage`, `NumberMessage`, `BlockExtention`) and `pkg/tron/core` (`NodeInfo`), responses
are proto-unmarshalled into the typed messages.

| Probe | REST today | gRPC |
|---|---|---|
| Latest block | `POST /wallet/getblock` `{"detail": false}` | `/protocol.Wallet/GetBlock` with `BlockReq{detail: false}` (no `id_or_num` = latest; detail false drops the transaction list) → header-only `BlockExtention`: height from `block_header.raw_data.number`, hash from `blockid`, parent from `block_header.raw_data.parentHash` |
| Finalized block | `POST /wallet/getnodeinfo`, parse `solidityBlock` `"Num:123,ID:..."` | `/protocol.Wallet/GetNodeInfo` → `NodeInfo.solidityBlock`, same string, same parser |
| Syncing | latest block number + timestamp, drift / 3000 ms vs `Lags.Syncing` | the same `GetBlock` header's timestamp, same arithmetic |
| Peers | `peerList` length vs `MinPeers` | `GetNodeInfo` `peerInfoList` length vs `MinPeers` |
| Lower bound | `POST /wallet/getblock` `{"id_or_num": N, "detail": false}`; `{}` or empty `blockID` = pruned | `/protocol.Wallet/GetBlock` with `BlockReq{id_or_num: "N", detail: false}`; empty `blockid` = pruned (java-tron answers a missing block with an empty message, no error status). Bound types: the same `tronSupportedBoundTypes` fan-out |
| Client labels | `configNodeInfo.codeVersion` | `GetNodeInfo` `configNodeInfo.codeVersion`, same version/type derivation |

Not provided, matching REST: settings validators (no chain-id check), methods
processor (nil), head subscription (`ErrUnsupportedHeadSubscriptions`),
`PauseHeadWhileSyncing` false. `CapDetectors` = `caps.DefaultCapDetectors`.

### Hash parity

The same Tron block id reaches nodecore in three encodings: `0x`-prefixed hex from the
json-rpc `eth_getBlockByNumber` (EVM specific, `NewHashIdFromString`), bare hex from
the REST `blockID` (`NewHashIdFromString`), raw bytes from the gRPC `blockid`
(`NewHashIdFromBytes`). All three must reduce to the same `HashId`, for the parent
hash as well. A table-driven three-connector parity test
(`TestTronHashEncodingsAgreeAcrossConnectors`) runs one block through the three
`ParseBlock` paths and asserts every hash and parent hash against
`blockchain.HashId(raw)`, never against a constructor under test, as
`TestCosmosHashEncodingsAgreeAcrossConnectors` does for cosmos.

### Components

- `internal/upstreams/chains_specific/tron_specific/tron_grpc_specific.go`:
  `TronGrpcSpecific` + `newTronGrpcSpecific`, `BlockProcessor`
  (`NewGenericBlockProcessor`), `GetLatestBlock`, `GetFinalizedBlock`, `ParseBlock`,
  `HealthValidators`, `LowerBoundProcessor`, `LabelsProcessor`, and the no-op
  methods above. Exported methods first, unexported below.
- `internal/upstreams/chains_specific/specific_helpers/tron.go`:
  `ParseTronSolidityHeight(s string) (uint64, error)` (moved out of the REST
  specific) and `TronSyncStatus(number, timestampMs, lag int64) protocol.AvailabilityStatus`
  (moved out of the REST syncing validator, together with the 3000 ms constant).
  Both REST and gRPC call sites switch to them.
- `internal/upstreams/validations/tron_validations/tron_grpc_health_validator.go`:
  `TronGrpcPeersValidator`, `TronGrpcSyncingValidator`.
- `internal/upstreams/lower_bounds/tron_bounds/tron_grpc_lower_bound.go`:
  `TronGrpcLowerBoundDetector` over `LowerBoundSearchCalculator`, same expansion into
  `tronSupportedBoundTypes`.
- `internal/upstreams/labels/tron_labels/tron_grpc_detector.go`:
  `TronGrpcClientLabelsDetector` implementing `labels.ClientLabelsDetector`.

### Error handling

The gRPC connector already converts statuses into response errors, so every probe
sees only `HasError()` and returns the error (an outage, retried by the generic
processors). The lower-bound probe is the one place a successful response carries a
verdict: an empty `BlockExtention` means pruned → `(false, nil)`; a non-empty one
means available → `(true, nil)`; a decode failure is an error. A non-positive latest
height is an error, as in REST.

### Tests

Mock connector (`mocks.ConnectorMock`) matched on the gRPC method name, answering
proto-marshalled messages:

- specific: parse of a `BlockExtention`, finalized height from `solidityBlock`,
  constructor rejects non-grpc connectors, factory `case` returns the gRPC specific
  for a grpc-only Tron upstream;
- health: syncing available / syncing / unavailable on error, peers immature below
  `MinPeers`;
- lower bound: pruned on empty message, available on populated one, error
  propagation, bound-type fan-out;
- labels: version and client type from `codeVersion`;
- helpers: `ParseTronSolidityHeight` table (valid, missing `Num`, garbage),
  `TronSyncStatus` boundary at `Lags.Syncing`;
- the three-connector hash parity test described above (json-rpc, rest, grpc).

## Verification

```
make test
make lint
```

Plus a smoke run against a java-tron node with `grpc` + `grpc-additional`
connectors: head advances, `has_grpc` is set, a `/protocol.WalletSolidity/GetAccount`
call over the ingress reaches the solidity port.

## Out of scope

- Caching or quorum for any gRPC method (nothing in the specs is cacheable).
- A gRPC head subscription for Tron (java-tron has no streaming RPC).
- Probing over `protocol.Database` or the solidity port.
- `Monitor` / `WalletExtension` services (not in the public specs).
