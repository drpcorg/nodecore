# Reject non-UTF-8 method names

Date: 2026-07-30
Branch: `reject_nonutf-8`

## Problem

RPC method names reach Prometheus label values and log lines. A client can put arbitrary
bytes there:

- JSON-RPC: `"method"` is a client-supplied JSON string. Neither `sonic` nor `encoding/json`
  rejects raw invalid UTF-8 bytes inside a string literal.
- REST: the method name falls back to `"<VERB>#/<restPath>"`, and `restPath` carries whatever
  the client percent-encoded, after echo has decoded it.
- WebSocket subscriptions: the `subscription` label of `nodecore_request_json_ws_connections`
  comes from `getSubscription`, which for `eth_subscribe` reads `params[0]` verbatim.

prometheus/client_golang validates label values unconditionally (`validateLabelValues`,
`labels.go:177-181`), and `WithLabelValues` panics on invalid UTF-8 (`counter.go:283-289`,
`gauge.go:237-243`) rather than letting a bad value into the registry — so the exposition
itself is never corrupted. What actually happens is worse: `execution_flow.go:262` calls
`requestTotalMetric.WithLabelValues(e.chain.String(), request.Method()).Inc()` as the first
statement of a detached `go func()` in `processRequest`, and there is no `recover()` anywhere
in `internal/`, `pkg/`, or `cmd/`. A panic there crashes the whole nodecore process. The same
class of crash applies to the subscription label at `internal/upstreams/ws/registry_commands.go:79`.
This change therefore fixes a remote-triggerable process crash — one HTTP request with a raw
invalid byte in `"method"` — not a metrics-hygiene issue.

Two pre-existing defects live in the same expression that has to be touched
(`internal/upstreams/ws/ws_protocol.go:109` `getSubscription`):

- `{"method":"eth_subscribe","params":[1]}` — `Raw()` returns `"1"`, so `sub[1:len(sub)-1]`
  is `sub[1:0]` and **panics** with `slice bounds out of range [1:0]`. Verified locally.
- `{"method":"eth_subscribe","params":[{"a":1}]}` — the label silently becomes `"a":1`.

## Rule

Reject a request if and only if a value that becomes a metric label is not valid UTF-8. In
practice that is the **method name** plus the **`chain` path parameter** (added in review — see
section 4). Nothing else is a reason to reject: invalid bytes in params, headers, query values,
or REST wildcard captures pass through untouched, because they never become a label value.

On the REST branch this rejects a request that is legal at the HTTP level — RFC 3986
percent-encoding may carry arbitrary octets, unlike RFC 8259 which requires JSON to be valid
UTF-8. Rejecting anyway is a deliberate call: sanitizing with `strings.ToValidUTF8` would keep
such a request forwardable, but it costs an allocation and a scan per request on a path an
attacker controls, and it makes the canonical method name lossy for stats and cache keys.

## Design

### 1. JSON-RPC — `internal/server/http_server/handlers.go`

In `NewJsonRpcHandler`, after the sonic unmarshal succeeds, walk `jsonRpcRequests` and return
a package-level sentinel error when any `Method` fails `utf8.ValidString`.

The check lives in the constructor, not in `RequestDecode`, so HTTP and WS are both covered in
one place and the request is refused before any work is scheduled.

Consequences, both deliberate:

- **Batches fail whole.** One bad method rejects the entire array with `protocol.ParseError()`
  (`http_server.go:198-205`). Per-entry rejection would need response-holder machinery inside
  `RequestDecode`; whole-batch rejection is how every other parse failure already behaves.
- **On WebSocket the connection closes.** `ws_server.go:100-104` logs a constructor error and
  breaks the read loop. This is the existing behavior for any parse failure and is kept as-is.

### 2. REST — `internal/server/http_server/rest_parser.go`

One check on `methodTemplate` immediately before `parseRestRequest` returns, covering both
branches of the switch. `rest_parser.go` and `handlers.go` share the `http_server` package, so
both sites return the same sentinel error.

The matched-spec-template branch passes trivially — templates come from the embedded spec JSON.
Only the `"<VERB>#/<restPath>"` fallback can reject. Junk bytes in a wildcard capture, a header,
or a query value do not reject the request; they flow into `RequestParams` and the upstream URL
as they do today.

### 3. WS subscription type — `internal/upstreams/ws/ws_protocol.go`

`getSubscription` gains an `error` return; `RequestFrame` (line 53) propagates it in the same
`fmt.Errorf("... cause - %s")` style as its neighbours.

```go
if request.Method() == "eth_subscribe" {
    ethSubType := jsonBody.GetByPath("params", 0)
    if ethSubType != nil && ethSubType.TypeSafe() == ast.V_STRING {
        if sub, err := ethSubType.Raw(); err == nil && len(sub) >= 2 {
            subType := sub[1 : len(sub)-1]
            if !utf8.ValidString(subType) {
                return "", errors.New(...)
            }
            return subType, nil
        }
    }
}
return request.Method(), nil // existing fall-through
```

- `TypeSafe() == ast.V_STRING` removes the `params:[1]` panic and the `params:[{"a":1}]`
  mislabel. This matches the repo convention (`integrity_methods.go:144`,
  `keydata/data.go:51`, `parse_response.go:40`).
- `len(sub) >= 2` is a defensive second guard: with `V_STRING` confirmed, `Raw()` is always at
  least `""`, but the slice expression should not depend on that invariant holding elsewhere.
- Non-string, missing, or unreadable `params[0]` falls through to `request.Method()`, which
  section 1 has already proven to be valid UTF-8. A non-string first param is therefore *not*
  a rejection reason — the request still reaches the upstream, which may reject it on its own
  terms.

Result: `subType` is valid UTF-8 on every path, so no raw/sanitized split, no second accessor,
and no placeholder label value is needed anywhere. `RequestOperation`, `BaseRequestOp`,
`registrySubscription`, and `request_registry.go` are unchanged.

An error out of `getSubscription` surfaces from `sendWsRequest` (`ws_processor.go:206-210`) as
an ordinary per-request upstream error: the client gets an error for that one subscribe, the
client connection stays open, and no metric series is ever created.

#### Escaped input

`Raw()` returns the escaped source text, so a body containing `"\ud800"` yields the six ASCII
characters `\ud800` and passes the check. That is correct — escaped text is valid UTF-8, so it
cannot make `WithLabelValues` panic and cannot crash the process. Only genuinely raw invalid
bytes are rejected, which is exactly the set that matters.

### 4. The `chain` path parameter — `internal/server/http_server/http_server.go`

Added in review. `chain := c.Param("chain")` is used verbatim as the label of
`wsConnectionsMetric` (`ws_server.go:58,61`) with no validation anywhere in between, so it is
the one remaining client-controlled string that reaches `WithLabelValues`. Every other `chain`
label comes from `chain.String()` on a resolved `chains.Chain` enum and is safe by construction.

The check goes in `requestHandler` **before `upgrader.Upgrade`**, not inside `HandleWebsocket`.
That placement matters: once the connection is hijacked, `net/http`'s `conn.serve` recover
skips its own cleanup (`if !c.hijacked()`), and `closeConn()` at `ws_server.go:158` is a plain
call rather than a deferred one — so a panic after the upgrade leaks the socket and its FD with
no read deadline, invisible to the gauge. Rejecting before the upgrade means no hijack, an
ordinary parse-error response, and nothing to clean up. It also covers the plain HTTP path,
which changes an invalid-UTF-8 chain's response from "chain … is not supported" to a parse
error.

An earlier draft of this document claimed the panic produced a clean connection drop with no
leak. That was wrong, for the hijack reason above.

#### Reaching it requires an uppercase escape

Only `%FF` is a repro, not `%ff`. Go's escaper emits uppercase hex, so `%FF` round-trips: the
re-escaped path equals the original, `url.Parse` leaves `URL.RawPath` empty, echo routes on the
decoded `URL.Path`, and the param carries the raw `0xFF` byte. With `%ff` the re-escaped form
differs, so `RawPath` is populated, echo routes on the encoded form, and the param is the three
literal ASCII characters `%ff` — valid UTF-8, no panic. Measured:

| request | `chain` param |
| --- | --- |
| `/queries/%FFeth/foo/%FFbar` | `"\xffeth"` |
| `/queries/%ffeth/foo/%ffbar` | `"%ffeth"` |

## Cost

Measured with `unicode/utf8` on Go 1.26 (Apple Silicon, 14 cores):

| input | time |
| --- | --- |
| `eth_getBlockByNumber` (20 B) | 3.9 ns |
| REST path template (74 B) | 8.1 ns |
| CJK method name (63 B, multi-byte) | 22.7 ns |
| 1 KB ASCII | 14.9 ns |
| 1 MB ASCII (pathological method name) | 11 µs |
| 1 MB with invalid first byte | 1.7 ns (early exit) |

No allocations — the strings already exist and `ValidString` does not copy. The pathological
case is bounded by work already done: sonic had to parse and allocate that same payload to hand
it over. Across a batch, the sum of method lengths is at most the body size, so validation stays
O(body) — a small constant factor on top of the parse that just ran. Rejection is the cheapest
path, so the check cannot be used as an amplifier. No length cap or fast-path guard is needed.

## Tests

`internal/server/http_server/handlers_test.go`

- single JSON-RPC request with an invalid byte in `method` → error
- batch where one entry has an invalid method → whole batch errors
- valid method (ASCII and multi-byte UTF-8) → unaffected
- invalid bytes in `params` → accepted

`internal/server/http_server/rest_parser_test.go`

- invalid-UTF-8 `restPath` on the fallback branch → error
- matched spec template with invalid bytes in a wildcard capture → **no** error
- invalid bytes in a query value or header → **no** error

`internal/upstreams/ws/ws_protocol_test.go` (`getSubscription`)

- `params:["newHeads"]` → `"newHeads"`, no error
- `params[0]` with raw invalid bytes → error
- `params:[1]` → no panic, falls back to `"eth_subscribe"`
- `params:[{"a":1}]` → falls back to `"eth_subscribe"`
- `params:[]` / missing `params` → falls back to `"eth_subscribe"`
- non-`eth_subscribe` subscribe method → returns the method name
- non-subscribe request → returns `""`

## Out of scope

- **Subscription label cardinality.** `subType` is client-controlled, so any distinct *valid*
  string already creates its own metric series. That is pre-existing and unrelated to UTF-8
  validity.
- Any other place a method name reaches a metric label or log line.
- Reworking `ws_server.go` so a per-message parse error answers with a JSON-RPC error instead
  of closing the connection.
- **gRPC/dshackle entry point.** `NativeCall` (`internal/server/emerald/native_call_adapter.go:76,141`)
  and `NativeSubscribe` (`internal/server/emerald/grpc_blockchain.go:151`) pass `item.GetMethod()` into the method name
  with no check of their own. `blockchain.proto` is proto3, and protobuf's proto3 string codec
  validates UTF-8 on the wire, so a malformed method name is rejected before nodecore sees it.
  `NativeSubscribe`'s params, however, arrive as a `bytes` field with no such validation, so
  that path depends entirely on the new `getSubscription` check. Recorded here so this doesn't
  later get "discovered" as a hole.
- **Unbounded `method` label cardinality.** `execution_flow.go:262` labels with
  `request.Method()` before the `SpecMethod() == nil` check, so every distinct *valid-UTF-8*
  unknown method mints a permanent series — and `GetSpecMethodWithFallback`
  (`pkg/methods/helpers.go:52-58`) never returns nil for client JSON-RPC traffic, so that nil
  check would not gate it even if the metric came after it. Same entry point and auth level as
  the crash, unbounded memory rather than a restart. Raised in review; a real follow-up, and
  bounding it needs more than reordering that one line.
- **No `recover()` in the detached goroutine** at `execution_flow.go:259`. This change removes
  the known trigger, but any future panic there still kills the process.
