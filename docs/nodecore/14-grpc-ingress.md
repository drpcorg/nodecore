# gRPC chain ingress

nodecore can serve a chain's **own gRPC API** natively: a gRPC client built from the chain's
published protos (or grpcurl/Postman) calls nodecore exactly as it would call a node, and the
request is routed through the same upstream-selection, scoring, retry, and hedging machinery as
HTTP traffic — then forwarded to an upstream over the [`grpc` connector](05-upstream-config.md#connectors).

The first gRPC-native chain is **Sui** (`sui.rpc.v2`, mainnet and testnet). The **Cosmos SDK
family** is served over gRPC too: every cosmos chain shares the SDK, IBC and CosmWasm query
services (`cosmos.*`, `ibc.*`, `cosmwasm.*`). The full method surfaces live in the
[method specs](11-method-specs.md): the `sui-grpc` spec (27 methods across 7 services) and the
`cosmos-grpc` spec (157 methods across 21 services), each imported by its chains' spec bundles.

This is a **different server** from the [gRPC API](12-grpc-server.md) on `grpc-port`: that one
speaks the dshackle-compatible `emerald.*` protocol for querying upstream/chain state; this one
speaks the chains' own protocols for client traffic. They have different auth models and can run
independently of each other.

## Enabling

Off unless `server.grpc-ingress-port` is set. TLS reuses the same `server.tls` block:

```yaml
server:
  grpc-ingress-port: 9095
  tls:
    enabled: true
    certificate: /path/to/cert.pem
    key: /path/to/key.pem
```

## Calling it

The target chain rides call **metadata** — it cannot go into the gRPC `:path` (the protocol owns
it), and service names cannot identify chains (every Cosmos-SDK chain shares the same services).
Metadata keys are case-insensitive.

| Metadata key | Meaning |
| --- | --- |
| `x-nodecore-chain` | **Required.** The target chain, by any of its short names (`sui`, `cosmos-hub`, `osmosis`, ...) |
| `x-nodecore-key` | Access key, when [key management](03-auth.md) is enabled — the metadata twin of the `X-Nodecore-Key` header |
| `x-nodecore-token` | Static token, when the `token` [request strategy](03-auth.md#request-strategy) is enabled |
| `authorization` | `Bearer <jwt>`, when the `jwt` request strategy is enabled |

Key scoping (allowed/forbidden methods, etc.) applies to full gRPC method names such as
`/sui.rpc.v2.LedgerService/GetObject`.

With grpcurl:

```bash
grpcurl -H 'x-nodecore-chain: sui' localhost:9095 list
grpcurl -H 'x-nodecore-chain: sui' localhost:9095 describe sui.rpc.v2.LedgerService
grpcurl -H 'x-nodecore-chain: sui' -d '{}' localhost:9095 sui.rpc.v2.LedgerService/GetServiceInfo
grpcurl -H 'x-nodecore-chain: cosmos-hub' -d '{}' localhost:9095 cosmos.base.tendermint.v1beta1.Service/GetLatestBlock
```

(add `-plaintext` when TLS is off, and `-H 'x-nodecore-key: ...'` when key auth is on)

With a generated client, point it at nodecore instead of a node and attach the metadata per call or
via an interceptor:

```go
ctx = metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui")
resp, err := ledgerClient.GetServiceInfo(ctx, &v2.GetServiceInfoRequest{})
```

## Reflection

The ingress serves standard gRPC **server reflection** (v1 and v1alpha) covering every chain
service the method specs declare, so schema-driven tools work without local proto files: `grpcurl
list`/`describe`, Postman's service browser, grpcui. Requests and responses are encoded against the
chains' real descriptors, exactly as a node would serve them, and every file in each service's
transitive import closure is resolvable by filename - so clients that fetch imports one
`file_by_filename` at a time (Postman does) load the schema cleanly too. Reflection is
endpoint-global (the advertised set does not depend on `x-nodecore-chain`), matching how a node
presents itself.

## Semantics

- **Pass-through bodies.** Request and response messages are forwarded byte-for-byte; nodecore
  never parses them. Client metadata is forwarded to the upstream minus nodecore's own
  credential/routing metadata (`x-nodecore-*`, `authorization` — consumed here, never forwarded),
  the reserved `grpc-*` family, hop-by-hop keys, and keys the connector config owns; upstream response metadata comes
  back filtered the same way — headers as headers, **trailers as trailers**.
- **Verbatim upstream errors.** A non-OK status from the upstream reaches the client with its
  original code, message, and typed `google.rpc.Status` details. Transient upstream failures
  (UNAVAILABLE, INTERNAL, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED, ...) are retried on other
  upstreams first, per the chain's failsafe config; UNIMPLEMENTED additionally bans the method on
  that upstream.
- **Server streaming.** Both streaming call types (`server-stream-subscription`,
  `server-stream-finite`, see [method specs](11-method-specs.md)) are served: the request message
  is forwarded, every upstream frame is relayed as it arrives (headers with the first frame,
  trailers with the status), and the call ends with the upstream's status — `OK` for a completed
  finite stream, the node's own status when it aborts, `UNAVAILABLE` when a node ends a
  subscription without a status. A client that cancels tears down the upstream stream. Streams
  are never retried or hedged.
- **nodecore-origin errors** use the closed gRPC code model:

| Condition | Status |
| --- | --- |
| missing/unknown `x-nodecore-chain` | `INVALID_ARGUMENT` |
| no available upstreams for the chain | `UNAVAILABLE` |
| authentication failed (token/JWT) | `UNAUTHENTICATED` |
| key missing/unknown/out of scope | `PERMISSION_DENIED` |
| method not in the chain's spec | `UNIMPLEMENTED` |
| rate limit exceeded | `RESOURCE_EXHAUSTED` |
| subscription ended by the node without a status, or client too slow | `UNAVAILABLE` |

## Current limitations

- Client-streaming and bidi methods are not part of any spec (absence is the rejection).
- gRPC streams are **pass-through**: identical subscriptions from several clients open several
  upstream streams (no aggregation yet).
- Responses are **not cached** and **quorum** is not available for gRPC methods yet.
