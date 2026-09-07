# NativeSubscribe for gRPC server streams — design

Date: 2026-09-04. Branch: `grpc_nativesub`. Follows
[2026-08-25-grpc-server-streaming-design.md](2026-08-25-grpc-server-streaming-design.md)
(connector, flow, chain ingress) and
[2026-08-28-native-call-grpc-items-design.md](2026-08-28-native-call-grpc-items-design.md)
(NativeCall gRPC items).

## Goal

Let a dshackle client open a gRPC server stream (Sui `SubscriptionService/Subscribe*`,
`LedgerService/List*`) through the existing `Blockchain.NativeSubscribe` RPC, the way it
already opens JSON-RPC subscriptions. Both stream kinds are served: live subscriptions and
finite streams. The traffic path stays bytes-only.

## What already exists

Everything below the emerald server is implemented and merged:

- Method specs carry `grpc.call-type` (`server-stream-subscription`, `server-stream-finite`);
  both make `Method.IsSubscribe()` true.
- `GrpcConnector.Subscribe` opens the upstream stream and emits `protocol.GrpcSubResponse`
  frames: data, error (verbatim upstream status), or clean end. Headers ride on the first
  frame, trailers on the terminal one.
- `SubscriptionRequestProcessor` with result-only framing turns those frames into the
  client-facing model (`protocol.SubscriptionResponseHolder` = `ResponseHolder` + `IsEnd()`,
  refactored in #361): a `SubscriptionEventResponse` per event (bare payload, carrying the
  frame's headers/trailers), one `SubscriptionEndResponse` (`IsEnd() == true`, no payload,
  trailers) for a clean end, or a `ReplyError` carrying headers/trailers for a failure. In
  result-only mode nothing else is ever emitted (the JSON-RPC ack is a `WsJsonRpcResponse` that
  only the WS framing produces). `resolveSource` gives every gRPC stream its own key, so streams
  are never shared.
- The chain ingress (`grpc_ingress/grpc_call.go`) and today's `NativeSubscribe` both end their
  stream with OK as soon as they see `IsEnd()`; the closed channel is only the detachment case.
- `processSubMethods` advertises gRPC stream methods in `ChainState.SubMethods`, so the
  `subscribeMethodSupported` check in `NativeSubscribe` already passes for them.
- `protocol.GrpcStatusOf` maps any flow error onto a `*status.Status`: upstream statuses
  verbatim with details, nodecore errors onto canonical codes.

## The gap

`NativeSubscribe` builds a JSON-RPC envelope for every method. For a gRPC method it runs
`json.Valid` over the proto bytes and fails, or with an empty payload builds a JSON-RPC
request that no upstream serves. The reply item had no place for upstream metadata, trailers
or a typed terminal status.

## Contract (`drpcorg/public` v1.3.0, `proto/blockchain.proto`)

```proto
message NativeSubscribeRequest {
    ...
    oneof data {
        GrpcSubRequestData grpc_data = 8;
    }
}

message GrpcSubRequestData {
    repeated KeyValue metadata = 1;   // call metadata to forward to the upstream
}

message NativeSubscribeReplyItem {
    ...
    oneof data {
        GrpcSubResponseData grpc_data = 9;
    }
}

message GrpcSubResponseData {
    repeated KeyValue metadata = 1;   // upstream initial metadata, on the first item
    repeated KeyValue trailers = 2;   // upstream trailers, on the final item
    bool final = 3;                   // stream ended; payload absent
    bytes status = 4;                 // serialized google.rpc.Status; empty on a clean end
}
```

`payload` on the request stays the serialized request message (no 5-byte wire prefix); an
empty payload is a valid empty message. `subscription_id` and `unsubscribe_method` are ignored,
as today.

### Why in-band, not the NativeSubscribe stream's own status and metadata

The chain ingress mirrors the upstream stream one to one because it owns the client stream.
NativeSubscribe cannot:

- Heartbeats are sent every 30 s while the node is silent. gRPC flushes headers with the first
  send, so a heartbeat before the node's first frame would flush empty headers and lose the
  node's metadata.
- The NativeSubscribe stream status already means "nodecore failed before dispatch" (unknown
  chain, method not advertised, bad session). Putting the node's status on the same channel
  would make every `UNAVAILABLE` ambiguous.

This is the same split NativeCall uses between item errors and stream failures.

### Why `final` and an always-populated `status`

- An empty serialized message is a valid zero-byte frame, so "payload is empty" cannot mean
  "stream ended". `final` is the explicit marker.
- A serialized `google.rpc.Status{code: OK}` is also zero bytes, so a clean end cannot be
  signalled by an OK status; `final` with an empty `status` is the clean end.
- The connector keeps the upstream's status bytes only when the status carried typed details,
  and nodecore-originated terminal failures (slow consumer, node closed a live subscription,
  upstream stream failed to open after dispatch) have no upstream bytes at all. `status` is
  therefore always built by nodecore as `proto.Marshal(GrpcStatusOf(err).Proto())`: upstream
  statuses come through byte-for-byte including details, nodecore failures arrive in the same
  shape with canonical codes. The client has one error vocabulary: `status.FromProto`.

### Wire sequence

`grpc_data` is set only on the items that have something to carry: the item bearing the
upstream's initial metadata (the first one) and the final item. A plain data item has no
`grpc_data` - the client knows which transport it asked for, so an empty marker would say
nothing. Heartbeat items are the existing `heartbeat: true` items with nothing else.

Finite stream with three messages:

1. `payload` = message 1, `grpc_data.metadata` = node headers, signature if a nonce was given.
2. `payload` = message 2, no `grpc_data`.
3. `payload` = message 3, no `grpc_data`.
4. `grpc_data.final = true`, `grpc_data.trailers` = node trailers, `status` empty. No payload,
   no signature.
5. The NativeSubscribe stream closes with OK.

A subscription the node aborts looks the same except the final item has a non-empty `status`;
the stream still closes with OK. A stream that fails before any message is a single final item
carrying `metadata`, `trailers` and `status`, then OK.

Client rules, in order: skip heartbeats; if `final`, take trailers, decode `status` if
non-empty, the stream is done; otherwise deliver the payload, taking `metadata` from it if
present.

## nodecore design

### Adapter split, mirroring NativeCall

`NativeSubscribe` keeps its pre-dispatch checks unchanged and in order: session, chain
resolution, `subscribeMethodSupported`, `signingUnavailable`. Failures there remain stream
statuses (`Unauthenticated`, `Unavailable`, `Unimplemented`, `Internal`).

After them it picks a `nativeSubscribeAdapter` from the spec, never from the presence of
`grpc_data` (metadata is optional):

```go
specMethod := specs.GetSpecMethod(chain.MethodSpec, request.GetMethod())
if specMethod != nil && specMethod.GrpcCallType().IsServerStream() { grpc adapter } else { json-rpc adapter }
```

```go
type nativeSubscribeAdapter interface {
    // BuildRequest turns the subscribe request into a flow request. An error is
    // a pre-dispatch failure and becomes the NativeSubscribe stream status.
    BuildRequest(chain *chains.ConfiguredChain, chainSupervisor upstreams.ChainSupervisor,
        request *dshackle.NativeSubscribeRequest) (protocol.RequestHolder, error)
    // SendReply renders one flow response. done reports that the stream is over
    // and the handler must return err (nil for a clean in-band end).
    SendReply(stream dshackle.Blockchain_NativeSubscribeServer, wrapper *protocol.ResponseHolderWrapper,
        nonce uint64, signer signature.ResponseSigner) (done bool, err error)
}
```

Files: `native_subscribe_adapter.go` (interface + pick), `native_subscribe_adapter_jsonrpc.go`
(today's `mapNativeSubscribeMethod` family and `mapNativeSubscribeError` moved verbatim),
`native_subscribe_adapter_grpc.go` (new). `grpc_blockchain.go` keeps the handler, the shared
heartbeat loop and the pre-dispatch checks.

### JSON-RPC adapter

Behavior unchanged, code moved:

- `BuildRequest` = `mapNativeSubscribeMethod` + `protocol.NewUpstreamJsonRpcRequest("0", ...)`.
  `errSubscribeMappingNotSupported` → `Unimplemented`, other errors → `Internal`, as today.
- `SendReply`: error wrapper → `(true, mapNativeSubscribeError(err))`; `IsEnd()` → `(true, nil)`
  (as today; WS sources never produce one); event → `nativeSubscribeReplyItem` sent, `(false, nil)`.
  Today's `!ok → Internal` branch on the `SubscriptionResponseHolder` assertion is dropped: after
  the error check the pipeline can only deliver a `SubscriptionEventResponse` or a
  `SubscriptionEndResponse` (see the gRPC adapter below), so the branch never fired.

### gRPC adapter

`BuildRequest`:

- `protocol.NewUpstreamGrpcRequest("0", request.GetMethod(), requestParams, request.GetPayload(),
  chain.MethodSpec, mapDshackleSelectors([]*dshackle.Selector{request.GetSelector()})...)`.
- `requestParams.Headers = server_ctx.SanitizeForwardedHeaders(keyValueListToMap(request.GetGrpcData().GetMetadata()))`
  — identical to the NativeCall gRPC adapter. A nil `grpc_data` yields no headers.
- Payload verbatim, never inspected; empty is a valid message.
- The method's spec must have a server-streaming gRPC call type, else
  `protocol.NotSupportedMethodError` → `Unimplemented`: the mirror of the NativeCall gRPC adapter's
  check, so a unary gRPC method can never be built as a non-subscription request and routed to the
  unary flow. A gRPC call type implies the grpc connector (spec validation ties them), so no
  separate connector check is needed.

`SendReply` maps every wrapper onto the in-band contract. Metadata and trailers come from
`protocol.ResponseMetadata(wrapper.Response)` on every wrapper; the flow places headers only
on the first frame and trailers only on the terminal one, so the adapter is stateless and
`grpc_data.metadata`/`trailers` are simply whatever the wrapper carried. A data item gets
`grpc_data` only when the wrapper carried metadata (`grpcSubResponseData` returns nil
otherwise); a final item always has it (`grpcFinalItem`).

| flow response | reply item | done |
|---|---|---|
| error wrapper (`HasError()`) | `upstream_id`, `grpc_data{metadata, trailers, final: true, status: proto.Marshal(GrpcStatusOf(err).Proto())}` | true, nil |
| end frame (`IsEnd()`) | `upstream_id`, `grpc_data{metadata, trailers, final: true}`, `status` empty | true, nil |
| event | `payload` = `ResponseResult()`, `upstream_id`, signature via `nativeSubscribeReplyItem`, `grpc_data{metadata}` only when the frame carried headers | false, nil |

Rows are checked in that order. After the error check, the flow can only deliver a
`SubscriptionEventResponse` or a `SubscriptionEndResponse`: the flow's own pre-processor failures
are `ReplyError`s, every `IsSubscribe()` request is routed to `SubscriptionRequestProcessor`, and
in result-only mode that processor emits events, one end frame, or a `ReplyError` (the ack is a
`WsJsonRpcResponse` that only the WS framing produces). There is therefore no "unexpected
response type" branch: `IsEnd()` lives on `SubscriptionResponseHolder`, so the check is written as
`if sub, ok := response.(protocol.SubscriptionResponseHolder); ok && sub.IsEnd()` (the shape the
chain ingress uses), and anything else is an event read through `ResponseHolder.ResponseResult()`.
The final item is never signed. `nativeSubscribeReplyItem` owns reply-building failures (today only
signing): it logs the cause and returns a fixed `Internal` gRPC status, which both adapters return as
is - callers never classify the error themselves.

### Shared loop

The reply loop moves out of the handler into a function that takes the adapter and the
response channel, so tests can drive it with a fake stream:

```go
func serveNativeSubscribe(stream dshackle.Blockchain_NativeSubscribeServer,
    responses <-chan *protocol.ResponseHolderWrapper, adapter nativeSubscribeAdapter,
    nonce uint64, signer signature.ResponseSigner, heartbeat time.Duration) error
```

Loop semantics are unchanged: `stream.Context().Done()` → nil; channel closed → nil; nil
wrapper/response → `Internal`; otherwise `adapter.SendReply`, returning `err` when `done`; the
heartbeat ticker sends `{heartbeat: true}` after `heartbeat` of silence and every item resets
`lastSent`.

`WithSubscriptionResultOnly(true)` and the flow construction stay as they are for both
adapters.

## Errors

| situation | client sees |
|---|---|
| bad session | stream `Unauthenticated` |
| unknown chain / no chain supervisor | stream `Unavailable` |
| method not in `SubMethods` (incl. unary gRPC methods) | stream `Unimplemented` |
| nonce set, signing not configured | stream `Internal` |
| node rejects the stream at open | final item, `status` = node status verbatim, metadata/trailers if any, then OK |
| node aborts mid-stream | final item, `status` = node status verbatim, trailers, then OK |
| node ends a live subscription cleanly | final item, `status` = `UNAVAILABLE` (SubscribeTotalFailure), then OK |
| node ends a finite stream cleanly | final item, `status` empty, trailers, then OK |
| client too slow | final item, `status` = `RESOURCE_EXHAUSTED`, then OK |
| no upstream after dispatch (selection failed) | final item, `status` = `UNAVAILABLE`, then OK |
| client cancels | upstream stream cancelled, handler returns nil |

## Dependency

`github.com/drpcorg/public` bumped to v1.3.0 in `go.mod` (`go get` + `go mod tidy`); no chain
changes, so `make generate-networks` is not required.

## Testing

`grpc_blockchain_test.go` / new `native_subscribe_adapter_grpc_test.go`, over the existing
`testNativeSubscribeStream` fake and a wrapper channel fed into `serveNativeSubscribe`:

- finite stream: three `SubscriptionEventResponse` then a `SubscriptionEndResponse` → four
  items, first with metadata, the middle two without `grpc_data`, last `final` with trailers and
  empty status, handler returns nil.
- mid-stream upstream error with details and trailers → final item whose `status` decodes via
  `status.FromProto` to the same code, message and details; trailers present; handler nil.
- zero-frame failure → a single final item with metadata, trailers and status.
- nodecore terminal failure (`SubscribeTotalFailureError`) → final item with `UNAVAILABLE`.
- empty-payload event → a data item, not `final`.
- heartbeat item has no `grpc_data`.
- signing: event signed when nonce set; final item never signed.
- JSON-RPC regression: error wrapper still ends the handler with the mapped status; an end
  frame ends it with nil; events carry no `grpc_data`.
- `BuildRequest` (gRPC): request type `Grpc`, method and payload verbatim (incl. empty),
  metadata sanitized into `RequestParams.Headers`, selector mapped, nil `grpc_data` OK.
- adapter pick: Sui `SubscribeCheckpoints` → gRPC adapter; EVM `newHeads` → JSON-RPC adapter.
- existing `TestMapNativeSubscribeMethod` and `TestNativeSubscribeUnauthenticated` keep passing.

## Docs

- `12-grpc-server.md`, NativeSubscribe: gRPC stream methods are served; request `grpc_data`
  metadata; the reply item contract (table above); gRPC streams are not coalesced; JSON-RPC
  subscriptions unchanged.
- `14-grpc-ingress.md`: one line pointing dshackle clients at NativeSubscribe for streams.

## Out of scope

Aggregation/sharing of gRPC streams; `subscription_id`/`unsubscribe_method`; quorum for gRPC;
any change to the JSON-RPC subscribe wire behavior; client-streaming and bidi calls.
