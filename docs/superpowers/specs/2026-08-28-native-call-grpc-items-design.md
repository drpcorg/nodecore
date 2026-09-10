# NativeCall gRPC items — design

Date: 2026-08-28. Branch: `grpc-nativecall`.

## Goal

Let a dshackle client send native gRPC calls (Sui `sui.rpc.v2.*` unary methods) to nodecore
through the existing dshackle `Blockchain.NativeCall` stream, exactly the way it
already sends JSON-RPC and REST items. Unary only: a gRPC item produces one
buffered reply item. The traffic path stays bytes-only (see
`2026-08-19-grpc-support-v1-design.md`, principle 1).

## Out of scope (separate tasks)

- **NativeSubscribe for gRPC server-stream methods** — the emerald server still
  builds a JSON-RPC envelope there; a gRPC branch is its own task.
- **Quorum for gRPC** — quorum params are only read from the HTTP query
  (`http_server.go`, `quorum.ParamsFromQuery`), so quorum is unreachable via
  NativeCall for every request type today. Enabling it needs a request-side
  contract (metadata on the ingress, a field/selector on NativeCall), a
  `protocol.Grpc` case in `flow.hasHttpConnector`, and a QR request-id
  convention with the drpc signer. Parked.
- Caching for gRPC (all Sui methods are `cacheable:false`).

## Contract change (`drpcorg/public`, `proto/blockchain.proto`)

```proto
message NativeCallItem {
    ...
    oneof data {
        bytes payload = 4;
        RestData rest_data = 8;
        GrpcData grpc_data = 9;
    }
}

// A native gRPC call. method carries the full method name
// ("/sui.rpc.v2.LedgerService/GetObject").
message GrpcData {
    bytes payload = 1;              // serialized request message; no 5-byte wire frame prefix
    repeated KeyValue metadata = 2; // call metadata to forward to the upstream
}

message NativeCallReplyItem {
    ...
    repeated KeyValue response_headers = 14;  // existing: upstream headers / initial metadata
    bytes error_as_is = 15;                   // existing
    repeated KeyValue response_trailers = 16; // NEW: upstream trailers (gRPC only today)
}
```

`response_headers` and `response_trailers` are set on both successful and
failed reply items. Trailers are kept separate because a gRPC client must
receive trailers as trailers (rate-limit hints etc. live there); folding them
into headers would be lossy.

The proto edit and the module release are done by hand by the maintainer;
nodecore then bumps `github.com/drpcorg/public`.

## Reply-item mapping for a gRPC item

### Success

| field | value |
|---|---|
| `succeed` | true |
| `payload` | response message bytes verbatim (`ResponseResult()`) |
| `chunked` / `final_chunk` | false — `chunk_size` is ignored: a unary gRPC reply is one message and `UpstreamGrpcRequest.IsStream()` is always false |
| `signature` | as today, over `payload` (`buildReplySignature`) |
| `upstream_id`, `upstream_node_version`, `finalization` | as today |
| `response_headers` / `response_trailers` | upstream metadata via the `HasResponseHeaders` / `HasResponseTrailers` capabilities |

### Error

The reply reuses the existing error fields; a gRPC item never fills
`error_data`.

| field | value |
|---|---|
| `succeed` | false |
| `item_error_code` | a **canonical gRPC code (0–16)**, never `ResponseError.Code` (which is `GrpcErrorCodeBase + code` for upstream statuses, and collides with the canonical range for nodecore's own codes) |
| `error_message` | the status message |
| `error_as_is` | serialized `google.rpc.Status` when the upstream attached typed details (`GrpcStatus.StatusProto`); empty otherwise |
| `response_headers` / `response_trailers` | from the error response when it carries them (`*ReplyError` does for e.g. RESOURCE_EXHAUSTED with rate-limit hints) |

The code/message come from one shared function: `grpcStatusFromResponseError`
moves from `internal/server/grpc_ingress/chain_ingress.go` into `protocol`
(exported, e.g. `protocol.GrpcStatusOf(*ResponseError) *status.Status`),
unchanged in behaviour:

- an upstream `GrpcStatus` in `ResponseError.Data` is replayed verbatim
  (`status.FromProto` of `StatusProto` when present, else code+message);
- nodecore's own errors map onto the closed 17-code model
  (`NoAvailableUpstreams`/`NoApiConnectors` → UNAVAILABLE, `AuthErrorCode` →
  PERMISSION_DENIED, `ClientErrorCode`/`WrongChain` → INVALID_ARGUMENT,
  `RequestTimeout`/`CtxErrorCode` → DEADLINE_EXCEEDED, `RateLimitExceeded` →
  RESOURCE_EXHAUSTED, `NoSupportedMethod` → UNIMPLEMENTED,
  `SubscribeTotalFailure` → UNAVAILABLE, default INTERNAL).

Result: the client owns no mapping table. For a failed gRPC item it does
`status.FromProto(error_as_is)` when `error_as_is` is non-empty, else
`status.New(item_error_code, error_message)`. The proto3 presence trap
(`Status{code:0}` serializes to zero bytes) does not bite: an error item never
carries code OK.

## nodecore changes

### `protocol`

- `GrpcStatusOf(respError *ResponseError) *status.Status` — the moved
  `grpcStatusFromResponseError`. `grpc_ingress` calls it instead of its local
  copy.

### `internal/server/emerald/native_call_adapter.go`

- `adapterFor`: `item.GetGrpcData() != nil` → `grpcNativeCallAdapter{}`.
- `grpcNativeCallAdapter.BuildRequest`:
  1. `grpc_data` missing → client error (mirrors the REST adapter).
  2. spec lookup `specs.GetSpecMethod(chain.MethodSpec, item.GetMethod())`;
     nil → `NoSupportedMethod` error item (same message shape as the ingress:
     `unknown method %s`), so an unknown method answers precisely instead of
     "no available upstreams".
  3. `specMethod.GrpcCallType().IsServerStream()` → client error
     "server-stream method %s must be called via NativeSubscribe" (the flow
     would otherwise route it as a subscription).
  4. metadata: `server_ctx.SanitizeForwardedHeaders(keyValueListToMap(md))`
     into `RequestParams.Headers` — same reserved-key filtering as the ingress.
  5. selectors via `mapNativeCallSelectors` as the other adapters.
  6. `protocol.NewUpstreamGrpcRequest(requestID, method, params, payload, chain.MethodSpec, selectors...)`.
     `chunk_size` is ignored (documented in a comment).
- `grpcNativeCallAdapter.SendReply` → `sendReply(..., passThroughStream)`; the
  stream branch is unreachable for gRPC.

### `sendReply` / `nativeCallErrorItem` (shared by all adapters)

- Metadata extraction switches from the `*protocol.GenericUpstreamResponse`
  type assertion to the `protocol.HasResponseHeaders` /
  `protocol.HasResponseTrailers` interfaces, and trailers are stamped on
  success and error items (`ResponseTrailers` field). JSON-RPC/REST items are
  unaffected (their responses have no trailers; headers behave as before —
  `*ReplyError` headers, previously dropped, now ride along too, which is
  harmless and correct).
- Error rendering branches on the request type: `wrapper.Response` for a gRPC
  request → `protocol.GrpcStatusOf(err)` fills `item_error_code` (canonical),
  `error_message`, and `error_as_is` (= `GrpcStatus.StatusProto` when set);
  `error_data` stays empty. Other request types keep the current
  `nativeCallErrorItem` behaviour exactly. The request type is known from the
  adapter (each adapter owns its `SendReply`), so no type sniffing of the
  response is needed: the gRPC adapter passes a gRPC error renderer into
  `sendReply`.
- Pre-dispatch failures (`BuildRequest` errors, signing unavailable) for gRPC
  items go through the same gRPC error renderer so `item_error_code` is a
  canonical code there too.

### Tests (`internal/server/emerald`)

- gRPC item success: payload verbatim, `response_headers` + `response_trailers`
  populated, signature over the payload when a nonce is given, not chunked even
  with `chunk_size > 0`.
- upstream status with typed details: `item_error_code` = canonical code,
  `error_message`, `error_as_is` round-trips through `status.FromProto`
  (details preserved), trailers present on the error item.
- upstream status without details: `error_as_is` empty, `error_data` empty.
- nodecore error (no available upstreams): `item_error_code` = UNAVAILABLE (14).
- unknown method → UNIMPLEMENTED (12); server-stream method → INVALID_ARGUMENT
  pointing at NativeSubscribe; missing `grpc_data` → INVALID_ARGUMENT.
- metadata sanitizing: reserved client headers are not forwarded.
- regression: JSON-RPC and REST items unchanged (`error_data` still filled,
  `response_trailers` empty).
- `grpc_ingress` tests keep passing against `protocol.GrpcStatusOf`.

## Order of work

1. `drpcorg/public`: proto edit, regenerate `pkg/dshackle`, release (manual).
2. nodecore: `go get github.com/drpcorg/public@<tag>`, `go mod tidy`.
3. `protocol.GrpcStatusOf` + ingress switch (independent of the bump; can go
   first).
4. gRPC adapter + `sendReply` metadata/error changes + tests.
