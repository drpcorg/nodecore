# gRPC server streaming — design

Date: 2026-08-25. Follows [2026-08-19-grpc-support-v1-design.md](2026-08-19-grpc-support-v1-design.md) (unary gRPC: connector, ingress, Sui polling) and [2026-08-24-generic-sub-response-design.md](2026-08-24-generic-sub-response-design.md) (transport-neutral `SubResponse`).

## Goal

Pass server-streaming gRPC calls through nodecore end to end: client → gRPC chain ingress → execution flow → `GrpcConnector` → node, and back, bytes-only. Two kinds of stream are supported: **subscriptions** (`sui.rpc.v2.SubscriptionService/Subscribe*`, live and unbounded) and **finite streams** (`LedgerService/List*`, a result delivered as several messages). The Sui head can additionally be driven by `SubscribeCheckpoints` instead of polling.

Explicitly out of scope: aggregation/dedup of gRPC streams (pure pass-through, one upstream stream per client), client-streaming and bidirectional calls, connector state events for gRPC (`SubscribeStates` stays nil), retries and hedges for any stream, extra observer/stats work beyond what WS events get today.

## Method specs

`grpc.call-type` gains two values replacing the current `server-stream`:

| value | meaning | `Method.IsSubscribe()` |
|---|---|---|
| `unary` (default when the block is absent) | one request, one response | false |
| `server-stream-subscription` | one request, unbounded live stream; a clean end from the node is a failure | **true** |
| `server-stream-finite` | one request, bounded stream; a clean end from the node is normal completion | **true** |

- `Method.IsSubscribe()` returns true for **both** streaming call types (as well as for `subscription.is-subscribe`). From the flow's point of view a finite stream *is* a subscription: a request answered by a stream of frames from one upstream, never retried or hedged, routed to `SubscriptionRequestProcessor`. Every consumer of `IsSubscribe()` (strategy, processor choice, method groups) therefore behaves identically for the two kinds; the only difference — the meaning of a clean EOF — is decided in exactly one place (`newGenericSourceBuilder`, see below) by reading `GrpcCallType()`.
- `specs.IsSubscribeMethod(specName, methodName)` (`pkg/methods/helpers.go`) currently reads the `Subscription` settings block directly — a second definition of "is a subscription". It becomes a thin wrapper over `GetSpecMethod(...).IsSubscribe()`, so there is one source of truth. Callers: `flow/request_processor.go` (would otherwise misclassify gRPC streams), `http_server/handlers.go`, `ws/registry_commands.go`, `emerald/grpc_blockchain.go` — the latter three are JSON-RPC-only and unaffected. `GetUnsubscribeMethod` keeps reading the `Subscription` block (only JSON-RPC subs have an unsubscribe pair).
- Existing validation for streams (not cacheable, no dispatch, no sticky) applies to both streaming types. `subscription` settings on a gRPC method are rejected (the call type is the single source of truth).
- `sui-grpc.json`: `SubscribeCheckpoints/Transactions/Events` → `server-stream-subscription`; `ListCheckpoints/Transactions/Events` → `server-stream-finite`. Other `List*` methods (`ListOwnedObjects`, `ListBalances`, …) are unary paginated calls and stay unary.
- `UpstreamGrpcRequest.IsSubscribe()` returns `SpecMethod().IsSubscribe()` instead of the hard-coded `false`.

Docs: `11-method-specs.md` (call types), `14-grpc-ingress.md` (streams now served), `05-upstream-config.md` (`head-mode`, gRPC connector paragraph).

## `GrpcConnector.Subscribe`

`Subscribe(ctx, request)`:

1. `conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, method, ForceCodec(rawGrpcCodec), MaxCallRecvMsgSize)`, then `SendMsg(body)` and `CloseSend()` — all synchronous. Any error is returned from `Subscribe` (mapped like the unary path: parent-ctx error vs. gRPC status). The 60s unary `requestTimeout` is **not** applied to streams; the caller's ctx is the stream's lifetime.
2. One receive goroutine per stream runs `RecvMsg` in a loop and pushes into a channel of `SubResponse` (buffer 16). It exits on `ctx.Done()`, on `io.EOF` (channel closed, nothing emitted), or on a status error (emits an error frame, then closes).
3. Returns `UpstreamSubscriptionResponse` with the channel and a uuid `OpId()`. `Unsubscribe(opId)` stays a no-op: cancelling the ctx passed to `Subscribe` is how a stream ends, and every caller (`newGenericSourceBuilder` via `srcCtx`, `SubscriptionHead` via its lifecycle ctx) already does that.

**Slow reader.** The connector blocks (`select { case out <- msg: case <-ctx.Done(): }`) and never drops or times out. Both consumers drain continuously: the subengine's fan-out goroutine (which disconnects a lagging *client* with `SubscriberTooSlowError` instead of stalling the source) and `SubscriptionHead` (reads inline). HTTP/2 flow control means an undrained stream pauses the node rather than buffering in nodecore, so nothing is ever lost.

**End of stream is a frame.** `SubResponse` gains a third kind next to data and error: `IsEnd() bool` — the clean end of a bounded stream (`WsResponse`/`GenericSubResponse` return false). The connector is kind-agnostic and never reads the spec: a message → data frame; a status error → error frame; `io.EOF` → end frame. Trailers ride on the terminal frame (error or end); headers on the first frame, whichever kind it is (a stream ending with zero data frames puts them on the end frame). No lookahead, no held-back frames.

**`GrpcSubResponse`** (new `SubResponse` implementation in `protocol`): `Message`, `Error *ResponseError` (carrying `GrpcStatus` exactly as unary errors do, so the ingress can replay the status verbatim), `End bool`, `UpstreamId`, `GetParsedEvent()` nil, plus `ResponseHeaders`/`ResponseTrailers` implementing `HasResponseHeaders`/`HasResponseTrailers` (filtered by `filterResponseMetadata`).

Errors from the stream are mapped the same way as the unary path (`protocol.NewGrpcUpstreamErrorResponse` equivalents), so `RESOURCE_EXHAUSTED` trailers etc. reach the client.

## Head: `head-mode`

New `Upstream.HeadMode` (`yaml:"head-mode"`), values `poll` / `subscribe`, default `subscribe`, validated in `Upstream.validate`. It is consulted **only** when the head connector is `grpc`; for other connector types it is ignored (the connector type decides, as today).

`blocks.createHead` stays where it is; `NewGenericHeadProcessor` reads `upConfig.HeadMode`. Selection for `specs.GrpcConnector`:

- `poll` → `NewRpcHead`.
- `subscribe` → call `specific.SubscribeHeadRequest()` once. `errors.Is(err, blocks.ErrUnsupportedHeadSubscriptions)` → `log.Warn` ("chain does not support head subscriptions, falling back to polling") and `NewRpcHead`; any other error is a real failure and is returned/logged as such (no silent downgrade); success → `NewSubHead`.

Other connector types keep today's mapping (json-rpc/rest/tendermint → `RpcHead`, websocket → `SubHead`).

**Unified sentinel.** `blocks.ErrUnsupportedHeadSubscriptions` is declared next to the `BlockChainSpecific` interface (`internal/upstreams/blocks/head.go`) — the package that owns the concept, and one every chain specific already imports (`chains_specific` → `blocks`), so there is no import cycle. Today the specifics disagree: eight declare a private `errUnsupportedHeadSubscriptions` (stellar, sui, cosmos-rest, bitcoin, tendermint, near, ton, starknet), four return ad-hoc `fmt.Errorf("… does not support websocket subscriptions")` (aptos, algorand, aztec, beacon), and tron REST returns `nil, nil` — a latent bug (a subscription head over it would call `Subscribe(ctx, nil)`). All 13 return the shared sentinel from both `SubscribeHeadRequest` and `ParseSubscriptionBlock`; the private copies and ad-hoc messages are removed. Sui stops returning it once it implements the real request.

No runtime fallback: if the node itself rejects the subscription (e.g. `UNIMPLEMENTED` because `SubscriptionService` is not enabled), `SubscriptionHead.Start` fails as it does for WS today. The docs state that such nodes need `head-mode: poll`.

`headNoUpdatesTimeout` selection (poll-interval-based for `RpcHead`, block-time-based for `SubscriptionHead`) and `OnNoHeadUpdates` (`Stop(); Start()`, which re-opens the gRPC stream) need no change.

### Sui

- `SubscribeHeadRequest()` builds a `SubscribeCheckpoints` request with `read_mask` limited to `sequence_number` (bytes marshalled from `pkg/sui` types).
- `ParseSubscriptionBlock` parses `SubscribeCheckpointsResponse` and takes the height from `cursor` (present on every frame, monotonic; on an unfiltered stream it equals the delivered checkpoint's sequence number). The `checkpoint` field is ignored, so progress-only frames — which the proto documents as occurring only on filtered streams anyway — need no special handling. A frame without a cursor is a parse error. Hashes stay synthetic (height-derived via `SyntheticHashes`) so the poll path (`getLatestBlock` on start) and the sub path produce a consistent parent-linked chain.
- The `BlockProcessor`/`CapDetectors` comments referring to "no streaming in v1" are updated; `CapDetectors` stays nil (no EVM-style caps for Sui).

## Execution flow / `SubscriptionRequestProcessor`

Both stream kinds reuse `SubscriptionRequestProcessor` — no new processor and no new branches: since `IsSubscribe()` is true for both, `createRequestProcessor` and `createStrategy` need no change (plain generic strategy, no failsafe → no retries or hedges, ever).

Changes inside the processor and `sub_aggregation.go`:

1. The `Subscription == nil` guard and the `Subscription.Method` read move into the non-result-only branch (JSON-RPC notifications need a method name; gRPC frames do not).
2. Sub-id generation and the `NewSubscriptionMessageEventResponse` announce frame are skipped when `subCtx.IsSubscriptionResultOnly()`; `subCtx.AddSub` is skipped too (there is no client-side unsubscribe by id over gRPC). The emerald server (also result-only) ignores that frame today, so it is unaffected.
3. Each emitted `SubscriptionResultResponse` carries the `SubResponse`'s headers/trailers when it implements `HasResponseHeaders`/`HasResponseTrailers` (`SubscriptionResultResponse` gets `WithResponseHeaders`/`WithResponseTrailers` like `GenericUpstreamResponse`).
4. `resolveSource`: every gRPC stream (both kinds) gets a key unique per request (`RequestHash|selectors|uuid`), so the engine never shares a gRPC source — pure pass-through, one upstream stream per client. This keeps the engine's fan-out/slow-consumer handling in place while deferring aggregation; a late joiner of a shared `ListCheckpoints` would otherwise miss the first messages. Enabling sharing later is a change to this key only.
5. `newGenericSourceBuilder` is the one place that knows the kind: an **end frame** is forwarded for `server-stream-finite` and converted into `SubscribeTotalFailureError` for a subscription (a node ending a live subscription is a failure). A channel closed without any terminal frame stays a total failure, as today.
6. `flow/strategy.go`: the `WsCapMatcher` (a subscription needs a live ws connector) applies to JSON-RPC subscriptions only — `request.IsSubscribe() && request.RequestType() != protocol.Grpc`. A gRPC stream rides the grpc connector the spec already binds the method to; `MethodMatcher` covers method support.
7. `subengine.Source` gains `Exclusive bool` ("never shared: tear down as soon as the subscriber leaves") set by the generic builder for gRPC streams, so a cancelled client releases the upstream stream immediately instead of after the 10s reuse grace that per-request keys can never benefit from.
8. The engine keeps the **terminal frame**, not just its error: `subscriber.terminal protocol.SubResponse`, `Subscription.Err()` derives from it, and `Subscription.Terminal()` exposes it. The processor's `terminalFailureWrapper` copies the frame's headers/trailers onto the `ReplyError`, so trailers on an upstream error frame (e.g. rate-limit hints on `RESOURCE_EXHAUSTED`) reach the client.
9. Engine: an end frame terminates the source like an error frame does (`terminate(ev)`), so `Subscription.Err()` is nil and `Subscription.Terminal()` is the end frame with its trailers. A channel closed without a terminal frame keeps today's meaning (total failure) — the locally-synthesized sources rely on it — so no `Finite` flag on `Source` is needed.
10. Processor: when `sub.Events` closes, `terminalWrapper(request, sub.Terminal())` emits the client's final response — the terminal error (a `ReplyError` carrying the frame's headers/trailers), or a `protocol.SubscriptionEndResponse` (non-event frame, no payload, trailers) for a clean end. The ingress already forwards metadata of every wrapper and skips non-event frames, so the end response sets the trailers and the following channel close returns `OK`; the emerald server and the WS path skip non-event frames too (and WS sources never emit end frames). `SubscriptionHead` treats an end frame like a closed channel (return; `OnNoHeadUpdates` resubscribes).

## gRPC ingress

`chainIngress.handle` selects a call shape from the spec method's call type through a private interface:

```go
type grpcCall interface {
    decode(stream grpc.ServerStream) (*server_ctx.Request, error)
    serve(stream grpc.ServerStream, handleResp *server_ctx.HandleResponse) error
}
```

`unaryCall` (today's decode + single-response serve) and `serverStreamCall` (same one-frame decode; streaming serve) are implemented now; the interface is the slot where client-streaming/bidi land later (those will also need `HandleRequest` changes — the interface is a seam, not readiness).

`serverStreamCall.serve` loops over `ResponseWrappers()`:

- error frame → forward headers/trailers, return `grpcStatusFromResponseError(...)`;
- event frame (`IsEventFrame()`) → `SetHeader` before the first `SendMsg` (headers ride on the first frame), `SendMsg(rawFrame)`; trailers seen on a frame are recorded and applied with `SetTrailer` before returning;
- non-event frame → skipped;
- channel closed → `nil` (OK) unless `ctx.Err() != nil`, then `status.FromContextError`.

The ingress builds the `SubCtx` with `WithSubscriptionResultOnly(true)` for streams. The current "server-streaming methods are not supported yet" rejection is removed.

## Errors

| situation | client sees |
|---|---|
| node rejects the stream at open (`Subscribe` error) | the node's gRPC status verbatim (details included) |
| node aborts mid-stream | the node's status, with headers/trailers forwarded |
| node ends a subscription cleanly | `SubscribeTotalFailure` → `UNAVAILABLE` |
| node ends a finite stream cleanly | end frame → `SubscriptionEndResponse` → `OK` with trailers |
| client too slow (engine fan-out buffer full) | `SubscriberTooSlowError` → mapped status |
| client cancels | stream ctx cancelled → upstream stream cancelled, nothing sent |

## Testing

- `grpc_connector_test.go` over bufconn: finite stream (N frames then EOF → N events, channel closed, headers on first / trailers on last), subscription aborted with a status (error frame carries status + trailers), open failure returned from `Subscribe`, ctx cancel stops the upstream stream, no 60s cap on streams.
- `head_processor_test.go`: `createHead` matrix (grpc × poll/subscribe × specific supports / returns the sentinel / returns another error; non-grpc connectors unchanged); every chain specific's `SubscribeHeadRequest`/`ParseSubscriptionBlock` returns `blocks.ErrUnsupportedHeadSubscriptions` where unsupported (tron REST included); `head_test.go`: sub head over a mocked connector parsing `SubscribeCheckpointsResponse` frames (cursor-driven; a frame without a cursor is an error).
- `sui_chain_specific_test.go`: `SubscribeHeadRequest` bytes and `ParseSubscriptionBlock`.
- `sub_processor_test.go` / `sub_aggregation_internal_test.go`: result-only mode without announce frame; per-request key uniqueness for gRPC streams; clean close → completion for finite vs. total failure for subscription.
- `chain_ingress_test.go`: end-to-end over bufconn for a finite stream (OK + trailers), a subscription aborted by the node (status replayed), a client cancel, and unary regression.
- `pkg/methods` tests: new call types parse/validate, `IsSubscribe` for `server-stream-subscription`, `subscription` block rejected on gRPC methods.
- Config test: `head-mode` default/validation.
