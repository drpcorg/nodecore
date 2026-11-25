# Emerald `Describe` — design

Date: 2026-11-25. Size: small change (one unary RPC, read-only).

## Intent

`Describe` is the emerald protocol's unary "what do you serve" call: the list of chains with their
availability, height, supported methods and labels. nodecore's `BlockchainService` answers it with
`Unimplemented`, so a client written against the emerald protocol cannot use it here.

Implement `Describe` with the same meaning it has in dshackle.

## Goals

- `Describe` returns one entry per chain nodecore serves, with availability, height, supported
  methods, supported subscriptions and node labels, plus the build version.
- Same authentication as the other `BlockchainService` RPCs.

## Non-goals

- `status.quorum` and `capabilities` stay empty.
- `excludedMethods`, `current_lower_block`, `current_lower_slot` stay empty (dshackle's `Describe`
  leaves them empty too).
- `SubscribeHead`, `SubscribeStatus`, `SubscribeNodeStatus` stay unimplemented. A dshackle instance
  that uses nodecore as a gRPC upstream needs those as well, and is not a supported setup.
- No change to `SubscribeChainStatus`.

## Anti-goals

- The response must not become a second source of routing truth. Chain discovery for clients stays
  on `SubscribeChainStatus`; `Describe` is a point-in-time read.

## Approaches

1. **Read each chain supervisor's current state (picked).** `Describe` walks
   `UpstreamSupervisor.GetChainSupervisors()` and maps `GetChainState()` to a `DescribeChain`. The
   state is an atomic snapshot, so the call takes no locks and starts no goroutines.
2. **Reuse the `SubscribeChainStatus` snapshot** (`toFullResponse`) and convert its events. Rejected:
   it ties a unary read to the event wire shape, and that path deliberately hides a chain until its
   first head arrives, which is wrong for a "what do you serve" answer.
3. **Leave it unimplemented and have clients take the snapshot from `SubscribeChainStatus`.**
   Rejected: that stream has no end-of-snapshot marker, so a client has to guess when the snapshot
   is complete; a unary call is the simpler contract.

## Design

### RPC

`GrpcBlockchainService.Describe(ctx, *dshackle.DescribeRequest) (*dshackle.DescribeResponse, error)`
in `internal/server/emerald/grpc_blockchain.go`:

1. `sessionAuth.requireSession(ctx)`; return its error as is.
2. No `appCtx` or no `UpstreamSupervisor`: `codes.Unavailable`, "upstream supervisor is not
   configured" (same as `SubscribeChainStatus`).
3. Return the response built by the function below.

### Response builder

New file `internal/server/emerald/describe.go`, one exported function taking the
`upstreams.UpstreamSupervisor`, same shape as `SubscribeChainStatus` in `sub_chain_status.go`.

`DescribeResponse`:

| field | value |
|---|---|
| `build_info.version` | `buildinfo.ProductVersion()` |
| `chains` | one `DescribeChain` per chain supervisor, see below |

`DescribeChain`, from `chainSupervisor.GetChainState()`:

| field | value |
|---|---|
| `chain` | the chain's grpc id (`chains.GetChain(...).GrpcId`) |
| `status.chain` | same grpc id |
| `status.availability` | `availabilityStatusToApi(state.Status)` |
| `status.quorum` | unset |
| `nodes` | `state.ChainLabels`: `quorum` = `Amount`, `labels` = the label map |
| `supportedMethods` | `state.Methods.GetSupportedMethods()` |
| `supportedSubscriptions` | `state.SubMethods` |
| `currentHeight` | `state.HeadData.Head.Height`; 0 while the chain has no head |
| `capabilities`, `excludedMethods`, `current_lower_block`, `current_lower_slot` | unset |

Rules:

- A chain with no head yet is listed, with height 0. This matches dshackle and differs from
  `SubscribeChainStatus` on purpose.
- A chain whose grpc id is 0 is skipped: it has no `ChainRef`, so a client can neither name it nor
  call it (dshackle filters the same way). *My call.*
- Output is deterministic: chains sorted by grpc id, methods and subscriptions sorted
  alphabetically. The supervisor map and the method sets iterate in random order, so without
  sorting two identical calls return differently ordered lists. *My call.*

### Shared mapping

`LabelsToApi` in `chain_event_mapper.go` already turns `[]AggregatedLabels` into
`[]*dshackle.NodeDetails` inline. Extract that part into an unexported helper and use it from both
`LabelsToApi` and the new builder. No behaviour change for the status stream.

### Docs

Add a `Describe` entry to the `BlockchainService` list in `docs/nodecore/12-grpc-server.md`: what
it returns, that it is a point-in-time read, which fields are left empty, and that live state
belongs to `SubscribeChainStatus`.

## Tests

- Builder: chain with a head (all mapped fields); chain without a head (listed, height 0); chain
  with grpc id 0 (skipped); two chains (sorted); labels mapped to `nodes`; `quorum` and
  `capabilities` empty. Reuse `fakeChainSupervisor` / `newChainState` from
  `sub_chain_status_test.go`.
- Service: session required when auth is enabled (`Unauthenticated` without a session);
  `Unavailable` without a supervisor.

## Left to the builder

- Function and helper names, test layout.
- Whether the sort lives in the builder or in small helpers.
