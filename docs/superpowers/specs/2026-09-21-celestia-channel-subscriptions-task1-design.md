# Celestia channel subscriptions, Task 1: WS ingress, upstream side, pushed heads — design

Date: 2026-09-21. Branch: `cel_sub`. Depends on `drpcorg/public` PR #272, released as v1.4.6 and
pinned here.

This is the first of two nodecore tasks for Celestia DA subscriptions. Task 2 is
`NativeSubscribe` passthrough, with its own spec and PR. Heads pushed over `header.Subscribe`
(originally a separate Task 3) are part of this task, ruled 2026-09-21.

## Goal

A client connected to nodecore's WS ingress (`/queries/celestia`) can call `header.Subscribe` and
`blob.Subscribe` the way it would against a celestia-node, receive `xrpc.ch.val` events, and cancel
with `xrpc.cancel`. Nodecore in turn holds the node-side subscriptions over a `websocket`
connector on a Celestia upstream, deduplicated by the existing per-chain engine like every other
node-backed subscription. A Celestia upstream whose head connector is `websocket` tracks its head
through `header.Subscribe` instead of polling `header.LocalHead`.

## The protocol (celestia-node = `filecoin-project/go-jsonrpc` channels)

- Subscribe is a plain call: `{"id":1,"method":"header.Subscribe","params":[]}` →
  `{"id":1,"result":7}`. The result is a per-connection `uint64` channel id, counting from 1.
- Events are notifications: `{"method":"xrpc.ch.val","params":[7, <value>]}`.
- Node-side close: `{"method":"xrpc.ch.close","params":[7]}`. Sent after a cancel, on node
  shutdown, and for `blob.Subscribe` when the reader is 16 messages behind.
- Cancel: `{"method":"xrpc.cancel","params":[<id of the ORIGINAL subscribe request>]}`. Never
  answered. An unknown id is silently ignored. An `id` on the cancel only logs a node-side warning.
- A go-jsonrpc client registers its channel handler only after the ack, so events written before
  the ack are dropped. Nodecore must write the ack before the first event (it already does).
- WS is served on the same port and path as HTTP behind the same bearer-token check.

## What already exists (dialect-agnostic, unchanged)

- Method specs (public v1.4.6): `celestia-websocket.json` declares `header.Subscribe` and
  `blob.Subscribe` with `subscription: {is-subscribe, type: "channel", method: "xrpc.ch.val",
  unsubscribe-method: "xrpc.cancel"}`; `xrpc.cancel` is `local: true` in `celestia-json-rpc.json`;
  the `celestia` bundle imports the websocket spec. `Subscription.Type` is `base` (default, filled
  in when absent) or `channel`. Config validation (`upstream_config.go:662`) therefore now accepts
  a `websocket` connector on a Celestia upstream.
- The engine (`subengine`, `sub_aggregation.go`) dedups node-backed subscriptions by
  method+params+selector and serves many clients from one upstream op.
- The upstream registry (`ws/registry_commands.go`) correlates the subscribe ack by nodecore's
  internal request id and files the op under `result` as the sub id. `7` on the ack and `7` in
  `params[0]` of an event both normalize to `"7"` through `ResultAsString`, so channel events
  correlate with no change.
- `SubscriptionRequestProcessor` (`flow/sub_processor.go`) attaches a per-client `subFraming` to
  the shared source and writes the ack before entering the event loop.
- The WS ingress (`http_server/ws_server.go`) writes every wrapper it receives and closes the
  connection when it sees a `ReplyError` with code `SubscribeTotalFailure`.

## Out of scope for Task 1

- A clean-end path for channel subscriptions. The engine keeps its rule that a node ending a live
  subscription is a failure (`sub_aggregation.go:319`). Ruled 2026-09-21: "for v1 it's ok to
  keep the engine as is".
- Nodes that mix dialects on one connection (Lotus). The protocol implementation is selected per
  chain, see below.
- `NativeSubscribe` (Task 2), `fraud.Subscribe`, `header.Subscribe` over the HTTP ingress (forwarded, the node answers with its own error, same
  as `eth_subscribe` over HTTP today).

## Design

### 1. Upstream side: `ChannelWsProtocol`

A second implementation of `ws.WsProtocol` in `internal/upstreams/ws`, next to
`JsonRpcWsProtocol`. Selected in `createConnector` (`upstream_factory.go`): a `websocket`
connector on a chain whose `Type` is `chains.Celestia` gets `NewChannelWsProtocol`, every other
chain keeps `NewJsonRpcWsProtocol`. The shared `protocol.ParseJsonRpcWsMessage` is not modified.

- `RequestFrame`: identical to the base implementation (replace `id` with the next internal
  numeric id, sub type is the method name). The body of `JsonRpcWsProtocol.RequestFrame` moves
  into an unexported helper both implementations call.
- `ParseWsMessage`: reads `method` first.
  - `xrpc.ch.val`: a `Ws` response with `SubId` = raw `params[0]`, `Message` = raw `params[1]`.
  - `xrpc.ch.close`: a `Ws` response with `SubId` = raw `params[0]` and
    `Error = protocol.SubscribeTotalFailureError()`. This is the only shape that produces a
    `Ws`-typed frame carrying an error; the registry uses that to recognize a node-side end.
  - Anything else (call results, the subscribe ack, errors): fall through to
    `protocol.ParseJsonRpcWsMessage`, so the existing `JsonRpc`/`Unknown` behavior is kept.
  - Malformed channel frames (`params` not an array of two, `params[0]` not a number) return an
    error, which the ws processor already turns into a disconnect, same as any unparsable frame.
- `DoOnCloseFunc`: writes `{"jsonrpc":"2.0","method":"xrpc.cancel","params":[<op id>]}` with no
  `id` field. The op id is the numeric id nodecore stamped on the subscribe request (`RequestFrame`
  sets it on the wire and `Register` uses the same value as the op id), so it is exactly what
  `xrpc.cancel` expects. It is not the channel id from the ack. The body is built directly, not
  through `NewInternalUpstreamJsonRpcRequest`, which would add `id: 1`. Same 5s write timeout and
  logging as the base hook.

### 2. Registry: node-side end

`subscriptionCommand.handle` (`registry_commands.go`) gains one branch: when the frame carries an
error, it writes the frame to every op filed under that sub id, deletes the sub from
`state.subs` and decrements `jsonRpcWsConnectionsMetric`. Everything after that is existing
machinery:

1. Each op's `Start` loop forwards the frame to its consumer and, because it carries an error,
   calls `Cancel` on the op.
2. `closeReq` sends a `finishCommand`, which no longer finds the sub and answers `false`, so the
   op's close hook does not run and no `xrpc.cancel` is sent for a channel the node already
   closed.
3. The engine source (`sub_aggregation.go`) sees the error frame, emits it as the terminal and
   returns. Its `stop` calls `Unsubscribe(opId)`, which finds the op already gone.

A late `xrpc.ch.close` or `xrpc.ch.val` for a sub id that is no longer filed hits the existing
unknown-sub drop. On an upstream disconnect nothing changes: `cancelAllCommand` already skips
the close hook.

### 3. Client side: `SubCtx` owns the dialect

`SubCtx` (`flow/sub_ctx.go`) is an interface, one instance per client connection, and the owner
of the client dialect:

```go
type SubCtx interface {
	Framing() subFraming                                                    // how a subscription is presented
	Unsubscribe(request protocol.RequestHolder, key string) ProcessedResponse // the reply to the client's unsubscribe call
	Exists(key string) bool
}
```

**Client contract (ruled 2026-09-22).** A subscription is filed with the connection at ack time,
in `begin`. A client cancels after it has the ack, as against a node; an `xrpc.cancel` sent before
the ack finds nothing and is ignored. A reservation-at-ingress scheme was tried and dropped as
too involved for the case.

`NewSubCtx(chain)` picks the implementation by chain type, Celestia gets the channel one, every
other chain the base one; `NewResultOnlySubCtx()` is for the gRPC ingress and the emerald server.
The WS server calls `NewSubCtx` with the connection's chain; the processor asks `subCtx.Framing()`
and no longer reads the spec's `subscription.type`. Ruled 2026-09-22: on the client side the
dialect is selected per connection by chain type, the same way the upstream protocol is, so the
spec field is informational in nodecore for now (a mixed-dialect node like Lotus would need it).

- **`baseSubCtx`**: the JSON-RPC model, unchanged behavior. A `CMap` from the generated
  subscription id to its cancel (generated ids never collide, so one key holds one
  subscription). `Framing()` is `jsonRpcFraming` (or `resultOnlyFraming` for the result-only
  instance). `Unsubscribe` cancels and answers `true`, unknown id included.
- **`channelSubCtx`** (`flow/channel_sub_ctx.go`): a mutex-guarded map from the client's own
  request id to a list of `{channelId, cancel}` (a client may reuse one request id for several
  subscribe calls) and the `uint64` channel id counter starting at 1. `Framing()` is
  `channelFraming`. `Unsubscribe` cancels every subscription under the key, oldest first, and
  replies with one `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[<chId>]}` per closed
  channel, in order, as a `SubscriptionResponse`; an unknown key gets nothing, as a go-jsonrpc
  node answers a cancel.

`subFraming` stays `begin(request, cancel)` + `event`; the processor owns the one subscription
context and releases it on exit. Both JSON-RPC framings refuse a method with no `subscription`
block (a gRPC stream method is `IsSubscribe` without one), so `event` never dereferences a nil
`Subscription`. `channelFraming.begin` type-asserts `protocol.RealIdHolder` (fails the subscribe
otherwise), files the cancel through `channelSubCtx.addSub(realId, cancel)`, which returns the
channel id, and acks `{"jsonrpc":"2.0","id":<client id>,"result":<chId>}`. `event` is
`{"jsonrpc":"2.0","method":"xrpc.ch.val","params":[<chId>, <payload>]}` through
`protocol.NewChannelSubscriptionEventResponse`, with the method from `Subscription.Method`.

Terminal frames go through the existing `terminalWrapper` for every dialect: a total failure
closes the connection as today, with no `xrpc.ch.close` first (dropped 2026-09-22; a go-jsonrpc
client closes its channels itself when the websocket closes).

**Accepted trade-off.** The `xrpc.ch.close` that answers a cancel is written by the local
processor's response path, not by the subscription's own goroutine, so a value that arrived in
the same instant as the cancel can reach the client after the close. A go-jsonrpc client logs
and drops a value for a channel it no longer knows. Documented on `channelSubCtx.Unsubscribe`.

### 4. Client `xrpc.cancel` (`flow/local_request_processor.go`)

The `subCtx != nil` block reads `params[0]`, normalizes it with `ResultAsString` (so a string id
`"sd"` matches a subscription filed under `sd`) and returns `subCtx.Unsubscribe(request, key)`.
No method switch: the base context answers `true` for `eth_unsubscribe` and friends, the channel
context answers the `xrpc.ch.close` frames for `xrpc.cancel`.

### 5. `protocol.RealIdHolder`

A small interface in `protocol/data.go`, `RealIdHolder { RealId() string }`, implemented only by
`UpstreamJsonRpcRequest` (which already has the method: the client's id unwrapped from quotes).
`RequestHolder` itself is unchanged, so the REST and gRPC holders need no stub. Consumers that
need the client's own id type-assert for it: the quorum header check in `execution_flow.go`
(which used an anonymous interface for the same thing) and the channel framing, which fails the
subscribe with an error if the request is not a JSON-RPC one. The channel framing needs the
client's own id because that is what the client will put into `xrpc.cancel`.

### 6. Celestia specific (`chains_specific/celestia_specific`)

- `NewCelestiaSpecific`: `specs.WebsocketConnector` is routed to `NewCelestiaChainSpecificObject`
  like `JsonRpcConnector` (the DA JSON-RPC API is the same over ws; unary calls go through the ws
  connector's `SendRequest`). The error message listing supported connectors is updated.
- `CapDetectors`: returns `caps.DefaultCapDetectors(upstreamId, input.WsConnector)` when
  `input.WsConnector` is not nil, nil otherwise. That grants `WsCap` while the ws connector is
  connected, which is what `NewWsCapMatcher` requires to route subscribe methods to the
  upstream. Comments saying the ws connector does not speak the channel protocol are removed.
- `SubscribeHeadRequest` returns
  `protocol.NewInternalSubUpstreamJsonRpcRequest("header.Subscribe", []interface{}{}, chain)`,
  mirroring the EVM `eth_subscribe("newHeads")` hook.
- `ParseSubscriptionBlock` parses the `xrpc.ch.val` value, an `ExtendedHeader`, through the
  existing `ParseBlock` (`specific_helpers.ParseCelestiaExtendedHeader`, the same shape
  `header.LocalHead` returns) and keeps a copy of the raw bytes in `RawData`, as the EVM hook
  does.
- No change to `createHead` (`blocks/head_processor.go`): a `websocket` head connector already
  builds `SubscriptionHead`, which fetches the current head once with `GetLatestBlock`
  (`header.LocalHead`) and then serves `header.Subscribe` through the head connector's
  `Subscribe`. That call goes through the same ws processor, `ChannelWsProtocol` and registry as
  client subscriptions (the head does not use the engine, same as EVM). A `ch.close` on the head
  channel arrives as the error frame of section 2, `serveSubscription` returns it, and the run
  loop resubscribes on the processor's next no-head-updates nudge, the existing behavior for a
  failed head subscription.
- Which connector drives the head follows the existing rules: `head-connector` when set,
  otherwise the "best" connector, `json-rpc` in default mode (polling) and `websocket` in
  strict mode (subscription). Operators who want pushed heads on a default-mode upstream set
  `head-connector: websocket`.

### 7. Metrics and logs

`json_ws_connections` keeps its `subscription` label; for channel methods the value is the method
name (`header.Subscribe`, `blob.Subscribe`), which `getSubscription` already returns for anything
but `eth_subscribe`.

## Data flow, end to end

Head of a Celestia upstream with `head-connector: websocket`: `SubscriptionHead.Start` →
`GetLatestBlock` (`header.LocalHead`) → `WsConnector.Subscribe` with `header.Subscribe` → op
`101`, ack `7` → each `xrpc.ch.val [7, header]` → `ParseSubscriptionBlock` → `headsChan`.

Client `{"id":1,"method":"header.Subscribe","params":[]}` on `/queries/celestia`:

1. `HandleRequest` builds the request (`IsSubscribe()` true from the spec) and the flow creates a
   `SubscriptionRequestProcessor`.
2. `resolveSource` gives the generic node-backed key; the engine finds or creates the source. A
   new source selects an upstream with `WsCap`, and `WsConnector.Subscribe` registers op `101`
   and writes `{"id":101,"method":"header.Subscribe","params":[]}`.
3. Node → `{"id":101,"result":7}`. `rpcCommand` files op `101` under sub `"7"`.
4. `channelFraming.begin` files the cancel under `"1"` in the `channelSubCtx` and the ingress
   writes `{"jsonrpc":"2.0","id":1,"result":1}` (client channel id 1).
5. Node → `{"method":"xrpc.ch.val","params":[7,{...}]}` → `ChannelWsProtocol.ParseWsMessage` →
   `subscriptionCommand` → op `101` → source → engine → client
   `{"jsonrpc":"2.0","method":"xrpc.ch.val","params":[1,{...}]}`.
6. Client `{"method":"xrpc.cancel","params":[1]}` → local processor →
   `channelSubCtx.Unsubscribe(request, "1")` cancels the subscription and replies
   `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[1]}`; the subscription's own stream just
   ends.
   `sub.Unsubscribe()` on the engine; when the last client detaches, the source `stop` →
   `Unsubscribe("101")` → close hook → `{"jsonrpc":"2.0","method":"xrpc.cancel","params":[101]}`
   to the node.
7. Alternative to 6, node → `{"method":"xrpc.ch.close","params":[7]}`: registry drops sub `"7"`,
   op `101` is cancelled without a close hook, the source emits the total failure, every client
   on that source gets the total failure and the connection is closed.

## Error handling

- Upstream-side: unparsable frames disconnect the upstream ws (existing). Node `ch.close` is a
  total failure for the source (section 2). Subscribe rejected by the node (error on the ack) goes
  through the existing `rpcCommand` error path unchanged.
- Client-side: `header.Subscribe` with no `WsCap` upstream fails with the existing
  no-available-upstreams error. `xrpc.cancel` with a malformed `params[0]` keeps the existing
  server error reply. `xrpc.cancel` with an unknown id gets nothing back.

## Testing

Unit tests, per site:

- `ws/ws_protocol_test.go` (or a new `channel_ws_protocol_test.go`): `RequestFrame` id stamping;
  `ParseWsMessage` for `ch.val`, `ch.close`, the ack, an error result, malformed `params`;
  `DoOnCloseFunc` writes `xrpc.cancel [opId]` without `id`.
- `ws/registry_commands_test.go`: an error `Ws` frame is delivered to all ops of the sub, the sub
  is dropped, the metric decremented, and `finishCommand` then answers `false`.
- `flow/sub_processor_test.go`: channel ack, event envelope, cancel through the `SubCtx` replies
  `ch.close` while the subscription stream ends without a failure, a terminal error is a plain
  total failure, channel ids count per connection; base behavior unchanged.
- `flow/sub_ctx_internal_test.go`: dialect selection by chain type; base `Unsubscribe` answers
  `true` (unknown id too); channel `Unsubscribe` closes every channel under a reused request id in
  order and answers nothing for an unknown id; channel ids count from 1 per context; both
  framings refuse a method without subscription info; the local processor over each dialect,
  including a string request id (`"sd"`).
- Celestia specific: ws connector accepted, `WsCap` detector present with a ws connector,
  `SubscribeHeadRequest` is a subscribe request for `header.Subscribe` with empty params,
  `ParseSubscriptionBlock` yields the height and hashes of an `ExtendedHeader` and keeps the raw
  bytes.
- `blocks/head_test.go` (or the existing subscription head test): a `SubscriptionHead` over a
  fake connector that answers the ack and then channel frames advances the head, and an error
  frame ends the run until the resubscribe nudge.
- `upstream_factory`: Celestia ws connector gets the channel protocol, an EVM one the base
  protocol.

Manual end-to-end, after the unit tests pass: nodecore with a Celestia mainnet upstream having a
`json-rpc` and a `websocket` connector on `wss://docs-demo.celestia-mainnet.quiknode.pro/`,
driven by a go-jsonrpc client against `/queries/celestia`: `header.Subscribe` delivers headers as
`xrpc.ch.val`, `xrpc.cancel` ends it with `xrpc.ch.close` and the connection stays usable for a
second subscribe. With `head-connector: websocket` on that upstream, the upstream head advances
from the subscription (log lines / `/metrics`) with no `header.LocalHead` polling after the
first fetch. Result reported with the actual frames.

## Docs

`docs/nodecore/05-upstream-config.md`: the `websocket` connector bullet in the connectors list
gains Celestia (the DA node's `header.Subscribe` / `blob.Subscribe` over go-jsonrpc channels,
and `header.Subscribe` as the head subscription). No per-chain deployment section.

## Files

- `internal/upstreams/ws/channel_ws_protocol.go` (new), `ws_protocol.go` (shared helper),
  `registry_commands.go`.
- `internal/upstreams/upstream_factory.go` (protocol selection).
- `internal/upstreams/flow/sub_ctx.go` (interface, constructors, `baseSubCtx`),
  `channel_sub_ctx.go` (new), `sub_framing.go`, `sub_processor.go`, `execution_flow.go`,
  `local_request_processor.go`; `internal/server/server_ctx/ingress_handler.go`,
  `http_server/ws_server.go`, `grpc_ingress/chain_ingress.go`, `emerald/grpc_blockchain.go`
  (constructor calls).
- `internal/protocol/data.go` (`RealIdHolder`), `subscription_response.go` (channel encoder).
- `internal/upstreams/chains_specific/celestia_specific/celestia_specific.go`,
  `celestia_chain_specific.go`.
- `docs/nodecore/05-upstream-config.md`.
- `go.mod`, `go.sum` (done).
