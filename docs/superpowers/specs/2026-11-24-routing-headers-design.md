# Routing headers: per-request upstream selection by group label or upstream id

**Date:** 2026-11-24
**Branch:** `pass_upstream_label`
**Status:** design agreed in discussion, ready for an implementation plan

## Problem

Which upstreams serve a request is decided entirely by config: the chain's balancing strategy
(`rating`, `base` or `label-balancing`) and, for an API key, its
`settings.upstreams.group-labels` restriction. A caller cannot say "serve this request from the
`archive` group and nowhere else", or, when verifying a node, "send this request to
`own-archive` and to no other upstream".

## Goals

1. `X-Nodecore-Group-Label: <label>` restricts a request to the upstreams carrying that config
   `group-labels` value.
2. `X-Nodecore-Upstream-Id: <id>[, <id>...]` restricts a request to the named upstreams.
3. Both are hard filters: no fallback to another upstream, on the first attempt, on a retry or
   on a hedge.
4. A request carrying either header is not routed by `label-balancing`; the chain's
   `balancing-strategy` (`rating` or `base`) orders the admitted upstreams.
5. The headers work on every client ingress that goes through the shared request pipeline:
   HTTP JSON-RPC, HTTP REST, WebSocket and the gRPC chain ingress.
6. The headers are opt-in for the operator (`server.routing-headers`, off by default) and are
   never forwarded to an upstream by the ingresses that read them.
7. Without either header nothing changes.

## Non-goals

- The emerald gRPC API is untouched. It has its own selector protocol and gets no new selector,
  and no emerald code changes: in particular the NativeCall REST adapter keeps forwarding the
  headers its client supplies exactly as it does today.
- No per-key permission for the headers; the switch is global.
- No bypass of health, method or rate-limit checks: an upstream that cannot serve is an error,
  not an exception to the rules.
- No cache bypass. A request with the headers reads and writes the cache like any other.
- No new balancing strategy.
- The local pending-transaction subscription sources keep ignoring upstream restrictions, as
  they already do for the key restriction.

## Key decisions

| # | Decision | Why |
|---|----------|-----|
| 1 | The headers become request selectors, appended to every request holder | Every path that builds its own candidate set already reads `request.Selectors()`: the integrity re-check, the local `newHeads`/`logs` sources, subscription sharing, dispatch fan-out, quorum. A strategy-only pin would be ignored by them. |
| 2 | The group header reuses `RequestGroupLabelSelector`; the id header adds one new selector and one new matcher | The group filter already exists for the key restriction. |
| 3 | No new strategy. `createStrategy` gets one extra condition: a request with a routing-header selector skips `LabelGroupStrategy` | The caller named what they want; group order, `pass-on-error` and `include-default` must not get in the way. The existing `rating`/`base` switch below still decides the order. |
| 4 | The header's group selector carries a mark, so it can be told from a key's | A key-restricted request without headers must stay under `label-balancing`, as documented in `03-auth.md`. |
| 5 | Group header: exactly one label, taken verbatim | Agreed. Keeps the header trivial. |
| 6 | Id header: one or more ids, any of them may serve; held as a set | Agreed. Useful for "one of these two nodes". |
| 7 | Both headers set: both selectors apply | Falls out of selectors; nothing is rejected and nothing is silently dropped. |
| 8 | `server.routing-headers`, default `false`. When off the headers are ignored | The headers let a caller override routing the operator configured, e.g. send all traffic to a group the operator ordered last. The operator opts in. |
| 9 | The headers are stripped from what the client ingresses forward to upstreams, whether the option is on or off | Group names and upstream ids are internal; an upstream operator must not receive them. |
| 10 | Cache is read and written as usual | Agreed. The restriction chooses which nodes answer, not what they answer. Documented, not coded. |
| 11 | The emerald NativeCall REST adapter is left as it is | Agreed. The headers mean nothing on the emerald API, and that path's header handling is out of scope. |

## Design

### 1. Header contract

| Header | Value | Meaning |
|--------|-------|---------|
| `X-Nodecore-Group-Label` | one label | admit upstreams whose config `group-labels` contain it |
| `X-Nodecore-Upstream-Id` | one or more upstream ids | admit upstreams whose config `id` is one of them |

Parsing, identical on every ingress:

- **Group label.** The header's values are joined with `", "` (repeated header lines are
  equivalent to one comma-joined line) and trimmed; the result is the label. No splitting: a
  value of `archive, full` is one label that no upstream carries, so the request fails with
  that cause.
- **Upstream ids.** Every value is split on `,`, each part trimmed, empty parts dropped; the
  rest form the set. Both `X-Nodecore-Upstream-Id: a, b` and two header lines give `{a, b}`.
  An upstream id containing a comma cannot be named this way; config does not forbid such ids
  and this spec does not start to.
- A header that is absent, or leaves nothing after this parsing (an empty value, or only
  separators such as `,`), is treated as not sent: the request is not restricted.

On the gRPC chain ingress the same names arrive as metadata keys (`x-nodecore-group-label`,
`x-nodecore-upstream-id`); repeated keys are the repeated-line case.

### 2. Selectors and matcher (`internal/protocol`, `internal/upstreams/flow`)

**`RequestGroupLabelSelector`** gains one field:

```go
type RequestGroupLabelSelector struct {
    Labels []string
    // FromRoutingHeader is set when the client asked for the group itself
    // (X-Nodecore-Group-Label) rather than an API key restricting it. Such a
    // request is not routed by label-balancing.
    FromRoutingHeader bool
}
```

Its `Key()` does not include the field: a key-restricted and a header-restricted subscription
to the same label admit the same upstreams and may share one upstream subscription (the
subscribe path never uses `LabelGroupStrategy`). The type's comment, which says clients cannot
send it, is updated.

**`RequestUpstreamIdSelector`** is new:

```go
// RequestUpstreamIdSelector admits an upstream whose config id is one of Ids.
// nodecore adds it for a client that sent X-Nodecore-Upstream-Id.
type RequestUpstreamIdSelector struct {
    Ids mapset.Set[string]
}
```

- `Key()` returns `upstream(<ids sorted, joined with "|">)`. Sorting is required: set iteration
  order is random and the key decides which subscriptions share an upstream subscription.
- The set is built once at parse time and never mutated, so concurrent reads from hedge
  attempts are safe.
- `LabelCacheKey` needs no change: its default branch already leaves routing-only selectors out
  of the cache key.

**`UpstreamIdMatcher`** in `flow/matchers.go` succeeds when the upstream id passed to `Match` is
in the set, otherwise returns a new `UpstreamIdResponse` of `SelectorType`. Its cause is a fixed
message that does not list the requested ids: the client knows what it sent, and the list is as
long as the client made it. `compileSelector` in `flow/selectors.go` gets one new case mapping the
selector to the matcher (without it the selector falls into the `unsupported` default).

`RequestGroupLabelSelector` keeps compiling to `GroupLabelMatcher` regardless of the new field.

### 3. Strategy choice (`flow/execution_flow.go`)

`createStrategy` changes in one place, the label-balancing check:

```go
if cfg := e.appConfig.UpstreamConfig.LabelBalancingFor(e.chain.String()); cfg != nil && !hasRoutingHeaderSelector(request.Selectors()) {
    return NewLabelGroupStrategy(...)
}
```

`hasRoutingHeaderSelector` is true when a top-level selector is a `RequestUpstreamIdSelector`, or
a `RequestGroupLabelSelector` with `FromRoutingHeader` set. The request then falls into the
existing `balancing-strategy` switch (`GenericStrategy` for `base`, `RatingStrategy` otherwise),
with the selector matchers already in `additionalMatchers`.

Everything above that check stays as it is and stays ahead of it:

- subscriptions use `GenericStrategy` with the selector matchers;
- quorum keeps its drpc-only `SpecificOrderUpstreamStrategy`, with the selector matchers
  combined in, because quorum responses are verified afterwards and only those upstreams sign;
- sticky-send keeps its `UpstreamIndexMatcher`, combined with the header matchers.

Consequences worth stating:

- A key with `settings.upstreams.group-labels` that also sends a header skips `label-balancing`
  too. Both the key's selector and the header's selector filter, so the request never leaves the
  key's upstreams; only the ordering among them changes. Requests of that key without headers
  are routed exactly as today.
- With several ids, the order among them is rating order on a `rating` chain. On a `base` chain
  it is whatever the base strategy's rotation over the chain's full upstream list yields, which
  is not an even split among a subset (with upstreams `a, b, c, d` and `{a, b}` admitted, `a`
  serves three requests of four). Nothing here promises a distribution; tests and docs assert
  only that the serving upstream is in the set.
- On a `rating` chain the candidate list is the rating registry's list, refreshed every
  `calculation-interval` (10s by default). An upstream added by a config reload can be named
  once the next rating round has run.

### 4. Ingress: from headers to selectors (`internal/server/server_ctx`)

The headers are visible in the ingress, before any request holder exists; the holders are built
inside `HandleRequest` by `RequestDecode`. The parsed selectors travel between the two as an
explicit argument, not through the context.

A new file `routing_headers.go` in `server_ctx` owns the two header names (private constants) and
the parsing of §1:

```go
// RoutingSelectors reads the routing headers through values (http.Header.Values
// or metadata.MD.Get) and returns the selectors they stand for, or nil when
// neither header is present.
func RoutingSelectors(values func(name string) []string) []protocol.RequestSelector
```

It returns a `RequestGroupLabelSelector` with one label and `FromRoutingHeader: true`, and/or a
`RequestUpstreamIdSelector`.

`HandleRequest` takes them as a new last parameter:

```go
func (a *ApplicationServerContext) HandleRequest(
    ctx context.Context,
    requestHandler RequestHandler,
    authPayload auth.AuthPayload,
    subCtx flow.SubCtx,
    routingSelectors []protocol.RequestSelector,
) *HandleResponse
```

In the loop that already runs `PostKeyValidate` per request holder, when `server.routing-headers`
is on and `routingSelectors` is not empty, they are appended to the holder through
`protocol.SelectorAppender.AppendSelectors`. A holder that does not implement it fails the
request, the same fail-closed rule `keydata.RestrictUpstreams` follows; all three holder types
(JSON-RPC, REST, gRPC) implement it today. The option is read in this one place: when it is off
the argument is ignored.

Each ingress parses once and passes the result down:

| Ingress | Parsed in | Passed through |
|---------|-----------|----------------|
| HTTP JSON-RPC and REST | the request handler closure in `http_server.go`, from `c.Request().Header.Values` | new parameter of `handleHttp`, then `HandleRequest` |
| WebSocket | the same place, from the handshake request | new parameter of `HandleWebsocket`, which passes it to `HandleRequest` for every message on the connection |
| gRPC chain ingress | `chain_ingress.go`, from `md.Get` | `HandleRequest` |

These are all the callers of `HandleRequest`; the emerald server builds its execution flow itself
and does not go through it.

### 5. Never forwarded (`server_ctx/forwarded_headers.go`)

Both header names are added to `reservedClientHeaders`. `SanitizeForwardedHeaders` applies that
set wherever a client ingress forwards client headers to an upstream: HTTP REST requests
(`rest_parser.go`) and gRPC chain ingress metadata (`chain_ingress.go`). JSON-RPC over HTTP and
WebSocket forward no client headers. Stripping does not depend on `server.routing-headers`.

On the emerald API, where the headers have no meaning, nothing is changed. The gRPC-data
adapters (`native_call_adapter_grpc.go`, `native_subscribe_adapter_grpc.go`) already use the same
sanitizer, so the two names are dropped there as a side effect of the shared set. The NativeCall
REST adapter (`native_call_adapter_rest.go`) does not use the sanitizer and keeps passing its
client's headers on unchanged.

### 6. Config (`internal/config/server_config.go`)

```yaml
server:
  routing-headers: true   # default false
```

`ServerConfig.RoutingHeaders bool`, `yaml:"routing-headers,omitempty"`. No validation, no
defaulting. Like the rest of `server`, it is read at startup and a change takes effect on
restart.

## Behaviour by request path

| Path | With a routing header |
|------|-----------------------|
| Plain unary request | Served by an admitted upstream; retries and hedges select only among admitted upstreams. |
| One upstream admitted | A retry or hedge finds no second candidate. The retry returns the first result; the hedge sends nothing. No other upstream is tried. |
| Cache | Read before any upstream is chosen and written after, as usual. A hit is answered with `response-provider: NoUpstream`. |
| Local methods (`eth_chainId`, `net_version`, unsubscribe) | Answered locally; the headers have no effect. |
| Dispatch (broadcast, maximum-value, not-null) | Fans out only to admitted upstreams. |
| Sticky create / sticky send | The create is restricted like any request. A send needs the filter's own upstream to be admitted too, else it fails. |
| Integrity re-check | Already honours selectors; with one admitted upstream the re-check finds no candidate and the first response is returned. |
| Quorum | The drpc-only candidate list is further filtered by the headers. |
| Batch | The headers apply to every item. |
| WebSocket | Fixed for the connection by the handshake; applies to every request and subscription on it. |
| Node-backed subscription | Subscriptions with different header sets never share an upstream subscription; equal sets share one. If the admitted upstream drops, the subscription ends as it does today when no upstream is left. |
| Local `newHeads` / `logs` | One source per distinct selector set, following only admitted upstreams. |
| Local pending-transaction sources | Ignore the headers (existing gap, see `03-auth.md`). |
| Emerald gRPC | No routing effect, no code change. |

## Errors

Nothing new is introduced. A request whose headers admit no usable upstream fails through the
existing selection error path:

- unknown id, id of another chain, or a label no upstream carries: `no available upstreams`,
  with the per-upstream cause trace when every upstream failed on a selector;
- admitted upstream unavailable, method unsupported or banned, rate limited: the same errors an
  unrestricted request gets when no upstream can serve.

Known imprecision, accepted: the reported reason is the most severe one across all upstreams of
the chain, not only the admitted ones (this is how selector misses are reported today). A typo
in an id on a chain where another upstream lacks the method is reported as the method being
unavailable.

## Documentation

- `02-server-config.md`: the `routing-headers` option and a "Routing headers" section with the
  header contract, the combination rules, the cache note (`response-provider: NoUpstream` on a
  hit), the WebSocket per-connection rule, and the statement that the HTTP, WebSocket and gRPC
  chain ingress never forward the headers to an upstream.
- `05-upstream-config.md`, `label-balancing`: a request with a routing header is not routed by
  label-balancing.
- `03-auth.md`, upstream restriction: how the key restriction combines with the headers.
- `14-grpc-ingress.md`: the two metadata keys.

## Testing

- **Parsing** (`server_ctx`): single value, repeated lines, comma-separated ids, whitespace,
  empty values, the group label kept verbatim, neither header present.
- **Selector and matcher**: `Key()` of the id selector is order-independent; the group selector's
  key ignores `FromRoutingHeader`; `UpstreamIdMatcher` hit and miss; `compileSelector` maps the
  new selector.
- **`createStrategy`** (`execution_flow_strategy_test.go`): on a label-balancing chain, a request
  with an id selector or a header-marked group selector gets the `rating`/`base` strategy; a
  key's group selector still gets `LabelGroupStrategy`; quorum and sticky branches unchanged.
- **`HandleRequest`**: the passed selectors are appended to every holder when the option is on;
  nothing is appended when it is off or when none are passed; a holder without `SelectorAppender`
  fails.
- **Forwarding**: both names removed by `SanitizeForwardedHeaders`.
- **e2e** (`test/e2e/http`, the existing Hardhat harness): group header and id header on a
  label-balancing chain with `pass-on-error: true` and `include-default: false` (retries stay in
  the set, an upstream outside `order` is reachable); both headers together; key restriction plus
  header; option off; a batch.
- **In-process, no new harness infrastructure**: the gRPC chain ingress with the metadata and the
  "upstream never receives either name" checks follow the existing patterns in
  `grpc_ingress/chain_ingress_test.go` and `http_server/rest_parser_test.go`; a WebSocket
  connection opened with the header is tested in `http_server`. The e2e harness has no gRPC
  ingress port, no REST upstream and no way to see the headers an upstream received, and this
  work does not add them.

## Left to the builder

- Naming of private helpers.
- Whether `hasRoutingHeaderSelector` lives in `execution_flow.go` or `selectors.go`.
- The exact wording of `UpstreamIdResponse.Cause()`.
- How `HandleRequest` reads the option when `AppConfig` or `ServerConfig` is nil in tests.
