# Generic subscription responses

**Date:** 2026-08-24
**Status:** designed, awaiting review
**Branch:** `grpc_stream`

## 1. Problem

`protocol.UpstreamSubscriptionResponse` is the contract every connector's
`Subscribe` returns, but its event type is the WebSocket wire struct:

```go
type UpstreamSubscriptionResponse interface {
	ResponseChan() chan *WsResponse
	OpId() string
}
```

`WsResponse` does two unrelated jobs today:

1. **WS wire message** inside `internal/upstreams/ws` — `Id` matches requests
   to responses in the registry, `SubId` routes notifications to ops, `Type`
   distinguishes RPC replies from subscription events.
2. **Generic subscription event** everywhere else — `SubscribeHead`,
   `sub_aggregation`, `logs_source`, `pending_tx_source`, `subengine`,
   `sub_processor` read only `Message`, `Error`, `UpstreamId`, `ParsedEvent`.

The WS-ness leaks into generic consumers in two places, both compensating for
job 1 bleeding into job 2:

- `blocks/head.go` checks `message.Type == protocol.Ws` to skip the WS
  subscribe-confirmation frame.
- `flow/sub_aggregation.go` swallows frames with `SubId == ""` for the same
  reason.

The upcoming gRPC server-streaming task cannot sanely produce `WsResponse`
values (fake `Type`, fake `SubId`), and its extra artifacts (headers,
trailers, terminal status) have no home. Earlier drafts that stored
headers/trailers as mutable fields on the subscription-response object were
rejected: the relay goroutine would set them concurrently with readers on a
"valid after X" temporal contract — a real smell. This task makes the
subscription contract transport-neutral **before** any gRPC streaming code is
written.

## 2. Goals / non-goals

**Goals**

- A transport-neutral event contract that WS implements today and gRPC
  streaming implements next, with no shared mutable state and no fake fields.
- Delete both WS leaks from generic consumers.
- No wrappers, adapters, or per-event conversion — existing values flow as
  the interface; generic code neither constructs nor names transport types.
- Behavior unchanged for clients (one deliberate exception, §7).

**Non-goals**

- The gRPC streaming connector, flow processor, ingress relay, and Sui head
  subscription — all stay in the follow-up task
  (see `docs/superpowers/plans/2026-08-24-grpc-server-streaming.md`, which
  must be revised on top of this spec before execution).
- Subscription aggregation/sharing for gRPC (separately parked).
- Reworking how the ws registry matches requests — wire matching stays as is.

## 3. The generic contract (`internal/protocol`)

```go
// SubResponse is one event of an upstream subscription/stream: either a data
// notification (GetMessage) or a terminal error (GetError). Implementations
// are transport-specific; consumers use only these accessors.
type SubResponse interface {
	GetMessage() []byte
	GetError() *ResponseError
	GetUpstreamId() string
	GetParsedEvent() ParsedEvent
}

type UpstreamSubscriptionResponse interface {
	ResponseChan() chan SubResponse
	OpId() string
}
```

There is **no conversion anywhere** — implementations flow through the
channel as the interface:

- `protocol.WsResponse` keeps its name and fields and gains the four getters
  — it implements `SubResponse` and the ws layer's frames flow up exactly as
  they do today.
- A **new** plain struct is added for events that *generic* code synthesizes
  (the sources' terminal-error and derived-data literals), so generic code
  stops constructing the ws wire type:

```go
// GenericSubResponse is the transport-neutral subscription event synthesized
// by the generic pipeline itself (sources, subengine, heads).
type GenericSubResponse struct {
	Message     []byte
	Error       *ResponseError
	UpstreamId  string
	ParsedEvent ParsedEvent
}
```

- Both implement the interface via `Get*` getters (the prefix also sidesteps
  Go's field/method name clash). The future gRPC frame type is a third
  implementation.
- Deliberately **no** setters, no `IsEvent()`, no headers/trailers on the
  contract: events arrive complete (§5), confirmation frames never reach
  consumers (§6), and gRPC metadata will live on the gRPC frame's own
  concrete type, stamped in-band before the frame is sent into the channel
  and read by the gRPC-specific relay via type assertion — channel ordering
  is the synchronization.

## 4. Renames

| Old | New | Note |
|---|---|---|
| `protocol.WsTotalFailureError()` | `protocol.SubscribeTotalFailureError()` | message text `"websocket total failure"` → `"subscription total failure"` (§7) |
| `protocol.WsSubscriberTooSlowError()` | `protocol.SubscriberTooSlowError()` | literal substitution would give `SubscribeSubscriberTooSlowError`; the prefix is dropped instead — the name is neutral without it |
| `protocol.WsTotalFailure` (error-code const) | `protocol.SubscribeTotalFailure` | numeric value unchanged (iota position untouched) |

WS-only names stay WS-named: `WsResponse`, `JsonRpcWsUpstreamResponse`,
`ParseJsonRpcWsMessage`, `WsConnected`/`WsDisconnected`, the `ws` package
internals.

## 5. UpstreamId stamped at the source

`GenericWsProcessor.startReader` stamps `UpstreamId = b.upstreamId` on every
frame right after `wsProtocol.ParseWsMessage` — the ws layer has the data,
so it builds the event complete. Consequences:

- `sub_aggregation.go` drops its `r.UpstreamId = upstreamId` enrichment, and
  the `SubResponse` interface needs no setter.
- Synthetic events built by the sources switch their struct literal from
  `&protocol.WsResponse{...}` to `&protocol.GenericSubResponse{...}` — same
  fields, so a type-name swap.

## 6. Confirmation frames never leave the ws layer

The successful subscribe confirmation's only job is `req.SetSubID` inside
`registry_commands.go`'s `rpcCommand.handle`; nothing above waits for it
(`WsConnector.Subscribe` returns the channel immediately). Change: for
subscribe ops with `Error == nil`, the registry performs the SubID bookkeeping
and **skips** the `req.Write` — the frame dies in the ws layer.

- **Error confirmations still flow**: they are the terminal signal
  `sub_aggregation` and `head.go` act on, and the registry's forwarder still
  cancels the op after an error frame.
- Non-subscribe RPC responses are unaffected (`IsSubscribeMethod` guard).
- Edge case, unchanged behavior: a successful confirmation carrying an empty
  sub id leaves the op hanging until disconnect/ctx today (consumers skipped
  the frame anyway) and still does; tightening that is out of scope.

This deletes both consumer-side hacks:

- `head.go`: the `message.Type == protocol.Ws` check goes away — the loop
  handles `GetError()` then parses `GetMessage()` unconditionally.
- `sub_aggregation.go`: the `r.SubId == ""` swallow goes away.

## 7. Behavior changes (client-visible)

Exactly one: terminal subscription errors now carry the message
`"subscription total failure"` instead of `"websocket total failure"`. The
numeric code is unchanged. Everything else — including
`ws_server.go`'s close-on-total-failure check against the renamed constant —
is behavior-preserving.

## 8. Mechanical churn (channel element type)

Go channels are not covariant: `chan *GenericSubResponse` is not assignable
to `chan SubResponse`, and no adapter goroutines are wanted. The channels
crossing the ws boundary and the generic pipeline's channels are declared as
`chan protocol.SubResponse`; writers put `*GenericSubResponse` values in.

- `internal/upstreams/ws`: the op's channels become
  `chan protocol.SubResponse` (`RequestOperation.Write`/`GetChannel` follow);
  the registry keeps writing the `*WsResponse` values it gets from the
  parser, and its forwarder checks `GetError()` instead of the field.
  `WsProcessor.SendWsRequest` returns the new channel type; `SendRpcRequest`
  returns `protocol.SubResponse` (its one consumer,
  `WsConnector.SendRequest`, only reads message/error via the getters).
- `internal/protocol`: `JsonRpcWsUpstreamResponse.messages`,
  `NewJsonRpcWsUpstreamResponse`.
- `internal/upstreams/flow`: `subengine.Source.Events`, subscriber channels
  and `Subscription.Events` in `engine.go`, the merged/out channels in
  `logs_source.go`, `pending_tx_source.go`, `sub_aggregation.go`,
  `subengine/heads.go`; `sub_processor.go`'s `responseUpstreamId` takes
  `protocol.SubResponse`.
- `internal/upstreams/blocks/head.go`: reads via getters.
- `pkg/test_utils/mocks`: `request_operation.go`, ws/connector mocks.
- Tests across the touched packages: rename + getter access; behavior
  assertions unchanged except deleting expectations of the confirmation frame
  reaching consumers.

## 9. Testing

- Existing suites are the safety net for the mechanical churn (`make test`).
- New/adjusted coverage:
  - ws registry: successful subscribe confirmation is not written to the op's
    response channel; error confirmation is; non-subscribe RPC responses
    still are.
  - ws processor: parsed frames carry the upstream id.
  - `head.go`/`sub_aggregation`: existing tests updated — no
    confirmation-skip fixtures needed anymore.
- `make lint` clean.

## 10. Follow-up (next task, out of scope here)

gRPC server streaming implements `SubResponse` with its own concrete frame
type; headers ride on the first frame and trailers/status on the terminal
one, stamped by the producing goroutine before send. The existing
server-streaming plan must be rebased onto this contract (its
`GrpcStreamUpstreamResponse` with `SetHeaders`/`SetTrailers` is superseded).
