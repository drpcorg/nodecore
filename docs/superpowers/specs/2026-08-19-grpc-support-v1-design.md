# gRPC support v1 (Sui, unary-only) — design

Design record artifact (diagrams, decision log, full discussion):
https://claude.ai/code/artifact/29929cbe-ac6f-43e7-8a17-bb8e41c1e2c0

## Goal

Native gRPC as a first-class upstream protocol, routed through the same
execution flow as JSON-RPC and REST. v1 is a self-contained vertical slice:

```
gRPC client (real Sui stubs) → nodecore catch-all ingress → execution flow → GrpcConnector → Sui node
```

Sui (`sui.rpc.v2`) is the first gRPC-native chain; the gRPC connector is the
upstream's **only** connector and owns heads, health, labels, and lower bounds.

**v1 requires zero emerald-grpc changes** — NativeCall/NativeSubscribe stay
untouched until the dproxy follow-up.

## Out of scope (separate follow-up tasks)

- **Server-streaming end-to-end** — its own design pass. Contract pieces
  already designed and parked: `NativeSubscribeRequest.metadata = 8`,
  `NativeSubscribeReplyItem optional bytes grpc_status = 5` (serialized
  `google.rpc.Status`; `optional` because `Status{code:0}` serializes to zero
  bytes — proto3 presence trap). Push heads via `SubscribeCheckpoints`.
- **dproxy ingress** — same catch-all handler shape (dproxy gets method specs
  too — settled); dispatches to NativeCall (with a new `GrpcData` oneof branch
  `grpc_data = 9`) / NativeSubscribe instead of the flow. gRPC errors in the
  contract reuse existing reply fields: `item_error_code` ← code,
  `error_message` ← message, `error_as_is` ← serialized `google.rpc.Status`
  when typed details exist.
- **Caching & quorum for gRPC** — both layer on byte hashes; nothing blocks them.
- **Client-streaming / bidi** — `UNIMPLEMENTED` at the edge, always.
- **Proto tag-parsers** — reserved notation `"tag-parser": "proto:<field-path>"`
  (field numbers, e.g. `proto:1.1.4`) for future height-aware routing.
- **Reflection-based method detection** — possible future `MethodsProcessor`.

## Core principles

1. **The traffic path is bytes-only.** A gRPC call on the wire is a method-name
   string (`:path = /sui.rpc.v2.LedgerService/GetObject`), serialized protobuf
   messages treated as black boxes (never parsed), metadata (= HTTP/2 headers),
   and a closed 17-code status model in trailers. That is exactly nodecore's
   `Method()` + `Body()` abstraction; no schemas anywhere in
   connector/flow/protocol layers. Cache keys are byte hashes.

   *Wire-framing note:* on the wire each gRPC message travels inside a 5-byte
   frame prefix (1-byte compressed flag + 4-byte big-endian length). That
   prefix is transport plumbing — grpc-go adds it on send and strips it on
   receive, below the codec layer — so `RequestHolder` bodies, the connector,
   and the ingress only ever see and produce plain `proto.Marshal` output and
   never construct or parse the prefix.
2. **Schema boundary (intent, not enforced).** Generated proto models live in
   `pkg/sui/` and exist for the chain-specific code
   (`internal/upstreams/chains_specific/sui_specific/`); the traffic path keeps
   working with bytes. Probes cross the boundary as bytes: `proto.Marshal`
   into a `RequestHolder` body, `proto.Unmarshal` on response bytes.
3. **Arity is not on the gRPC wire.** Unary vs streaming is not encoded in the
   protocol — only stubs know. Method specs are the only possible source, in
   nodecore and (later) dproxy alike.
4. **Chain and auth ride metadata**: `x-nodecore-chain`, `x-nodecore-key`. Chain cannot
   go in `:path` (gRPC owns it), and service names cannot identify chains
   (every Cosmos-SDK chain shares them).

## Task 1 — method-spec format + sui specs

New extension point in `pkg/methods/data.go`:

```go
type MethodSettings struct {
    // ... existing fields ...
    Grpc *GrpcSettings `json:"grpc"`
}

type GrpcSettings struct {
    CallType GrpcCallType `json:"call-type"`
}

type GrpcCallType string

const (
    GrpcCallTypeUnary        GrpcCallType = "unary"
    GrpcCallTypeServerStream GrpcCallType = "server-stream"
)
```

- **Default: absent `grpc` block / empty `call-type` = unary.** Only streaming
  methods carry an annotation.
- Method `name` is the full method string (`/sui.rpc.v2.LedgerService/GetObject`) —
  exact-match lookup, no templates, no verb prefix.
- Validation (existing `validate()` pattern): `call-type` ∈ {unary,
  server-stream}; `grpc` block only in specs whose `api-connectors` include
  `grpc`; gRPC method names match `/package.Service/Method` shape;
  `server-stream` mutually exclusive with `sticky`/`dispatch`/`cacheable:true`.
- `spec.api-connectors: ["grpc"]` already parses (`GetApiConnectorType`).
- Files: `sui-grpc.json` (plain, connector `grpc`) + `sui.json` (bundle
  importing it), method list enumerated from docs.sui.io during implementation.
  Server-streaming methods ARE listed (annotated, `cacheable: false`) so the
  ingress answers "server-streaming not supported yet" instead of "unknown
  method"; v1 ingress rejects them with `UNIMPLEMENTED`.
- Client-streaming/bidi methods are never listed — absence is the rejection.
- How `call-type: server-stream` relates to the existing `Subscription`
  settings / `IsSubscribeMethod` is the streaming task's question, not v1's.

## Task 2 — protocol plumbing

- `UpstreamGrpcRequest` implementing `RequestHolder` (`internal/protocol/`):
  `Method()` = full method string, `Body()` = the serialized request message
  exactly as the client produced it (the 5-byte gRPC frame prefix never
  appears at this layer — see the wire-framing note above), metadata in
  `RequestParams.Headers`, `RequestType() = Grpc` (enum exists), request hash
  over method + body + selector label key (mirroring `calculateRestHash`'s
  injective framing).
- gRPC status → `protocol.ResponseError` mapping. Two-track rule:
  classification drives routing; the original status (code + message + typed
  details) rides through verbatim. Mapping:
  - client, no retry: CANCELLED, INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS,
    FAILED_PRECONDITION, OUT_OF_RANGE
  - retry on another upstream: UNKNOWN, INTERNAL, DATA_LOSS, UNAVAILABLE, ABORTED
  - retry/hedge eligible: DEADLINE_EXCEEDED
  - throttled (retry elsewhere, feeds rating): RESOURCE_EXHAUSTED
  - ban method on upstream, retry elsewhere: UNIMPLEMENTED
  - upstream auth/config problem, pass through: PERMISSION_DENIED, UNAUTHENTICATED

## Task 3 — GrpcConnector (unary only)

`internal/upstreams/connectors/grpc_connector.go`:

- Config shape unchanged: `type: grpc`, `url: host:443`, `headers` (sent as
  per-call metadata), `ca` (TLS). One long-lived `grpc.ClientConn` per
  connector; HTTP/2 multiplexing + gRPC keepalives.
- `SendRequest`: `conn.Invoke(ctx, method, reqBytes, &respBytes,
  grpc.ForceCodec(rawCodec))` — a passthrough codec whose `Name()` returns
  `"proto"` (content-type stays `application/grpc+proto`, the node's stubs
  decode our bytes as the normal protobuf they are). Forward client metadata
  minus a deny-list (reserved `grpc-*` keys + hop-by-hop), mirroring the HTTP
  connector's header discipline. Errors arrive as `*status.Status` regardless
  of codec (they travel in trailers).
- **Response metadata passthrough**: capture the upstream's initial metadata
  and trailers via the `grpc.Header(&md)` / `grpc.Trailer(&md)` call options
  on `Invoke`. Headers ride the existing `HasResponseHeaders` capability
  (`GenericUpstreamResponse.WithResponseHeaders`); trailers get a sibling
  optional capability (e.g. `HasResponseTrailers`) that only the gRPC ingress
  consumes — a gRPC client must receive trailers *as* trailers
  (`SendHeader` / `SetTrailer` on the server stream). Both are filtered
  through a deny-list before forwarding: the HTTP connector's
  `defaultResponseHeaderDeny` set (Server, Set-Cookie, Content-Length,
  Content-Encoding, hop-by-hop, ...) extended with gRPC transport-owned keys —
  `content-type` and the reserved `grpc-*` family (`grpc-status`,
  `grpc-message`, `grpc-encoding`, `grpc-accept-encoding`), which the
  transport manages itself. Everything else (chain-specific metadata,
  rate-limit hints, request ids) passes through. Operators extend the list via
  the existing `ResponseHeaderDeny` connector config.
- `Subscribe`: returns not-supported (streaming task).
- `SubscribeStates`: returns `nil` in v1. Mapping `ClientConn` connectivity
  states onto connector-state events differs from the websocket model and is
  deliberately left to a later task.

## Task 4 — Sui chain family

Three sub-pieces:

1. **Registry**: Sui entry with `grpcId` in `drpcorg/public/chains.yaml`
   (user does this manually, in advance) + `make generate-networks`.
2. **Models pipeline** (no official Sui Go package exists — we compile the
   published protos ourselves; for future chains, check for a published Go
   module first, e.g. Cosmos's `cosmossdk.io/api`, and only vendor when none exists):

   - **Layout** — third-party proto submodules live under a dedicated parent
     folder, one subfolder per chain family, each with its buf template beside it
     (`emerald-grpc/` stays at the root — it is our own contract, a different
     category):

     ```
     chain-apis/
       sui/            ← submodule → MystenLabs/sui-apis, pinned to a release commit
       sui.gen.yaml    ← buf v2 template for it
     pkg/
       sui/            ← generated output, committed
     ```

   - **One make pattern rule serves every chain** — per-chain differences
     (proto root inside the vendor repo, subtree to generate, output path) are
     encoded in each chain's `.gen.yaml` via buf's `inputs:` section, never in
     the Makefile:

     ```make
     %-proto-gen:
     	buf generate --template chain-apis/$*.gen.yaml
     ```

     `make sui-proto-gen` today; a future chain adds its submodule + yaml and
     its target already works, zero Makefile edits.

   - **Generator version comes from `go.mod`, not from yaml.** `protoc-gen-go`
     is tracked as a Go 1.24+ tool dependency
     (`go get -tool google.golang.org/protobuf/cmd/protoc-gen-go`) and invoked
     by buf as `local: ["go", "tool", "protoc-gen-go"]`. Generator and protobuf
     runtime are the same module at the same version, so they can never drift;
     dependency updates bump both together, and the next regen surfaces the
     diff in the committed files. (Rejected: pinning a remote plugin version in
     the yaml, and unversioned "latest" remote plugins — the latter reintroduces
     generator/runtime drift.)

   - **Message types only, no client stubs** (chain families build
     RequestHolders, they never dial). Inputs restricted to the `sui/rpc/v2`
     subtree — `sui-apis` also carries `v2beta*` generations we must not
     generate or commit. The WHOLE subtree is generated deliberately (settled
     during implementation after evaluating trims): the probes only need the
     `ledger_service.proto` closure to compile, but the ingress serves gRPC
     reflection from the descriptors these files register, and reflection
     must cover every routable service — tools like grpcurl/Postman encode
     real requests against them, so partial or hand-written descriptors are
     not an option. `pkg/sui/*.pb.go` is marked linguist-generated.

   - Contributor requirements: the Go toolchain + the `buf` binary, and only
     when regenerating — generated code is committed, so builds and CI need
     neither.
3. **`sui_specific` — member by member.** All probes are unary gRPC through
   the connector; typed requests/responses via `pkg/sui`. The single source is
   `LedgerService/GetServiceInfo` (empty request — zero bytes on the wire);
   its response fields, verified against docs.sui.io (API graduated
   v2beta2 → sui.rpc.v2): `chain_id`(1), `chain`(2), `epoch`(3),
   `checkpoint_height`(4), `timestamp`(5), `lowest_available_checkpoint`(6),
   `lowest_available_checkpoint_objects`(7), `server`(8).

   | `ChainSpecific` member | v1 implementation |
   |---|---|
   | `GetLatestBlock` | Call `GetServiceInfo` → block at `checkpoint_height` with SYNTHETIC hash/parent hash (height-derived, parent-linkable — the Stellar/Solana pattern; revised during implementation: GetServiceInfo exposes no digest, and checkpoints are BFT-final so height-derived ids are safe), `RawData` = response bytes. |
   | `GetFinalizedBlock` | Same as latest — an executed checkpoint *is* final; Sui has no separate finalized pointer. |
   | `ParseBlock` | `proto.Unmarshal` into `GetServiceInfoResponse` → same mapping as `GetLatestBlock`. |
   | `ParseSubscriptionBlock` | Unsupported error (no head subscription in v1). |
   | `SubscribeHeadRequest` | Unsupported error — the poll-only pattern used by Stellar/Aptos today. |
   | `HealthValidators` | One validator issuing `GetServiceInfo`: transport/status error → `Unavailable`, otherwise `Available`. (Sui exposes no syncing flag; timestamp-lag-based `Syncing` detection is a possible later refinement.) |
   | `SettingsValidators` | Chain match: `GetServiceInfoResponse.chain` vs the configured network ("mainnet"/"testnet") — the EVM chain-id validation analog. `chain_id` (genesis digest) available for a stricter check if wanted. |
   | `CapDetectors` | None (empty slice). |
   | `LowerBoundProcessor` | From the same poll: `lowest_available_checkpoint` → data lower bound, `lowest_available_checkpoint_objects` → objects lower bound. |
   | `LabelsProcessor` | Parse ONLY `server`(8), split on the first `/`: left = client type, right = version — `"sui-node/1.78.0-03113679fb97"` → client `sui-node`, version `1.78.0-03113679fb97`. Detector under `internal/upstreams/labels/sui_labels/`, following the existing convention (Stellar's detectors also trim the `-<commit>` suffix from versions — same trim optional here). No other labels. |
   | `BlockProcessor` | Standard poll-based head processor, as other poll-only families use. |
   | `MethodsProcessor` | `nil` — no introspection in v1 (server-reflection idea parked). |

   No other Sui methods are called by probes in v1 — `GetCheckpoint` etc. are
   client-traffic-only. (If a future probe fetches checkpoints, remember Sui's
   `read_mask` behavior: it defaults to returning only `sequence_number,digest`.)

## Task 5 — ingress

REVISED during implementation (user decision): the ingress runs as a
**separate gRPC server** on `server.grpc-ingress-port` (0/absent = disabled —
the port replaces the earlier config-gate idea), reusing the same `server.tls`
config; the dshackle server on `grpc-port` is untouched. This was the
original design's own named fallback, promoted proactively: completely
different clients, auth models and codecs; keepalive enforcement scoped to
ingress connections only; and each port advertises its own coherent
reflection surface. Either server can run without the other.

1. **Delegating codec.** Background: a codec is the translator between wire
   bytes and Go values — every message a `grpc.Server` receives or sends
   passes through exactly one server-wide codec; there is no per-handler
   codec. The ingress server still hosts two kinds of handlers with
   incompatible needs: the **reflection service** (generated code that needs
   the normal proto codec) and the catch-all (which needs raw bytes). The
   codec always receives the *value the handler passed* to
   `RecvMsg`/`SendMsg` — generated code always passes proto structs, our
   catch-all always passes a private `*rawFrame` type — so the value's type
   says with certainty which world the message belongs to:

   ```go
   func (c delegatingCodec) Unmarshal(data []byte, v any) error {
       if frame, ok := v.(*rawFrame); ok { // the catch-all's raw type
           frame.data = data               // → pass bytes through untouched
           return nil
       }
       return c.protoCodec.Unmarshal(data, v) // generated handlers → proto codec
   }
   // Marshal mirrors this.
   ```

   Implement as `encoding.CodecV2` wrapping the registered proto `CodecV2`
   instance (`grpc.ForceServerCodecV2`). Since the codec no longer touches
   the dshackle path, the former benchmark obligation is void; the known
   semantic edge remains for THIS server only: per-content-subtype codec
   lookup is disabled (gzip is unaffected — compressors are a separate
   registry).
2. **Reflection** (added during implementation — the reason full `pkg/sui`
   generation exists): the ingress server registers reflection (v1 and
   v1alpha) with a custom `ServiceInfoProvider` that advertises, on top of
   its own registered services, every gRPC service the loaded method specs
   declare — filtered to symbols whose descriptors are compiled into the
   binary, so `ListServices` never claims what `FileContainingSymbol` can't
   serve. Descriptors come from the global protobuf registry (each generated
   package's init() registers them on import); the explicit anchor is
   `chain_descriptors.go` — one blank import per chain's model packages —
   guarded by a test asserting every spec-declared service resolves in the
   registry. This is also the generic path for module-backed chains (e.g.
   Cosmos's `cosmossdk.io/api`): no vendoring or codegen, just blank-import
   the service packages the chain's spec declares. grpcurl and Postman work
   against the ingress exactly as against a native node. Descriptors are
   endpoint-global, not per `x-nodecore-chain` — matching how a node presents
   itself.
3. `grpc.UnknownServiceHandler`: `grpc.MethodFromServerStream` → verbatim method.
4. Namespace guard: `emerald.*` → `UNIMPLEMENTED` immediately (it belongs to
   the dshackle server on `grpc-port`, never to a chain).
5. Metadata → chain (`x-nodecore-chain`) + auth (`x-nodecore-key` through the
   client-facing `AuthProcessor`; independent from dshackle session auth).
6. Spec lookup → call type. Unknown method or (v1) `server-stream` →
   `UNIMPLEMENTED`.
7. **Read exactly ONE request message and dispatch immediately — never wait
   for the client's half-close.** grpc-go's own `processUnaryRPC` does the
   same (one `recvAndDecompress`, then handler; verified in v1.83.0
   server.go). Extra frames from rogue clients are never read (HTTP/2 flow
   control bounds them); the response + trailers finish the stream. A client
   that never sends is bounded by a first-message receive deadline (keepalive
   enforcement only rate-limits pings and detects dead peers - it does NOT
   bound a live, silent open stream; corrected during review).
8. Build `UpstreamGrpcRequest` → `GenericExecutionFlow` → forward the
   upstream's filtered response metadata (`SendHeader` for headers,
   `SetTrailer` for trailers — from the response holder's
   headers/trailers capabilities), one `SendMsg` with the response bytes;
   errors returned as the upstream's verbatim `*status.Status`.

## Ordering

Task 1 first (everything references the spec format). Then 2 → 3 and 4 in
parallel (4's probes depend on 3's connector; the registry/models sub-pieces
don't). Task 5 last.

## Testing

Approach validated during design by three since-removed in-repo demos (raw
codec Invoke/NewStream against a stub server; typed client → catch-all →
upstream with spec-based arity; schema-less wire parsing + benchmarks). For
implementation: unit tests per task; connector and ingress tested against
in-process grpc-go servers over `bufconn` with real generated stubs on the
peer side (the pattern the demos proved); `sui_specific` probe tests with
`pkg/sui`-marshaled fixtures.
