# Valid-UTF-8 method names via a RequestMethod type — design

- **Date:** 2026-07-29
- **Status:** Implemented — branch `sanitize_method`
- **Area:** `internal/protocol` (new `request_method.go`, `data.go`, both request
  implementations, `request_observer.go`), plus the metric call sites in
  `internal/upstreams/flow`, `internal/caches`, `internal/dimensions`,
  `internal/upstreams/ws`, and `pkg/utils/strings.go`

## 1. Problem

A method name containing invalid UTF-8 reaches
`prometheus.CounterVec.WithLabelValues`, which validates label values and turns
the error into `panic(err)`. There is no `recover()` anywhere in `internal/` or
`pkg/`, and echo's `middleware.Recover()` is not registered — so **one request
kills the process**, i.e. a crash loop on a public endpoint. The production
crash was the REST path `GET#/` plus byte `0xC0`.

Two client-reachable entry vectors:

1. **REST** — `internal/server/http_server/rest_parser.go` builds
   `fullPath := req.Method + protocol.MethodSeparator + "/" + restPath` from the
   URL-decoded path and uses it as the method name whenever no spec template
   matches. RFC 3986 §2.5 leaves the encoding of percent-decoded octets
   undefined, so `%80` is not a malformed request — just not UTF-8.
2. **JSON-RPC body** — sonic's `ConfigDefault` sets `ValidateString: false` and
   `sonic.Valid` only checks syntax, so `{"method":"eth_\x80"}` decodes into a
   Go string carrying the invalid byte. No unusual URL needed, any chain.

(gRPC `NativeCall` is *not* a vector: protobuf-go rejects invalid UTF-8 in
proto3 string fields at unmarshal time.)

Secondary damage beyond the panic: the observer is built with the raw name and
`AddResult` runs even on the no-upstream failure path, so the name flows to
`internal/stats/base_stats_service.go` → `internal/integration/drpc_integration.go`
`proto.Marshal`, where `string method = 3`
(`internal/stats/protobuf/stats_request.proto`) requires valid UTF-8. One bad
request can fail an entire `StatsBatch` for an API key.

## 2. Goal

Make it structurally hard for a client-supplied method name to reach code that
requires valid UTF-8, while keeping the name byte-exact everywhere correctness
depends on it (routing, spec lookups, method matching, cache keys, rate-limit
keys, upstream URLs).

### Non-goals

- **Label cardinality.** `nodecore_request_requests_total{chain,method}` and
  `errors_total{chain,method}` take the client's method before any validation, so
  N distinct paths still mint N series. This change makes every label always
  *valid*, not *bounded*. Note the upstream-side metrics are **not** a bounded
  alternative: because the dimension hook also fires for no-upstream results
  (§5), each distinct unknown name mints a
  `nodecore_upstream_requests_total{…,upstream="NoUpstream"}` series plus a full
  `nodecore_upstream_request_duration` histogram (~16 series) plus a
  never-evicted `upstreamDimensionsMap` entry holding a quantile tracker — memory
  as well as series count, for any unknown name, valid UTF-8 or not. All of that
  is pre-existing and unchanged here; bounding it changes dashboards, so whoever
  picks it up must have dimensions in scope and not just the two counters.
- Rejecting invalid-UTF-8 method names at the HTTP boundary. RFC 3986 §2.5 leaves
  percent-decoded octet encoding undefined, so it would ban something permitted,
  and it would cover only the method name — query params and headers may
  legitimately carry non-UTF-8 bytes and are forwarded as they are.
- Changing anything under `internal/stats` or `internal/integration`.

## 3. Key decisions (settled → implemented)

| # | Decision | Choice |
|---|---|---|
| 1 | Where to fix | The **value**, not the sinks. `RequestHolder.Method()` returns a `RequestMethod` struct built once per request, so the safe form travels with the raw one — struct field, local, helper parameter |
| 2 | Accessor naming | `Name()` / `ValidUTF8Name()`. Named after the *guarantee*, not a consumer: the safe form has two unrelated consumers (Prometheus panic, proto3 marshal failure), so `LabelName()` would mis-scope the stats use. Also echoes `strings.ToValidUTF8`, the call that implements it |
| 3 | `String()` method | **None, deliberately.** Every call site names the form it wants; nothing is implicitly sanitized |
| 4 | Sanitizer | `strings.ToValidUTF8(s, "�")` via `utils.ToValidUTF8` — one definition of the replacement character. Returns valid input unchanged with no allocation, and collapses each invalid *run* to a single `�` |
| 5 | Where to sanitize | Only where invalid bytes actually break something — 12 conversions feeding 13 label arguments and one proto-bound field (§5). Logs and client-facing error text keep the raw form |
| 6 | Observer and results | Carry the **whole `RequestMethod`** — `RequestObserver.method`, `RequestResult.withMethod`, `UnaryRequestResult.method` and `GetMethod()` are all the struct. The conversion happens where the value is consumed (the stats key builder), so it is obvious at the point of use which form is wanted instead of being hidden in a setter |
| 7 | Comparability | Plain comparable struct, so it can still serve as a map key |
| 8 | Type name | `RequestMethod`, not `Method` — `pkg/methods` already exports `Method`, and `RequestHolder` has `Method()` and `SpecMethod() *specs.Method` one line apart |

On decision 3, the trade-off accepted knowingly: omitting `String()` gives **no
compile error** at the 22 `%s` sites, because `%s` on a struct of two strings is
legal Go and `go vet` accepts it, printing `{eth_call eth_call}`. That is loudly
wrong rather than silently wrong — it surfaces on the first log read or test run —
and `grep -n '\.Method()[,)]'` is the standing guard. The alternative
(`String()` returning the safe form) would have absorbed those 22 sites for free
but would have made the implicit path silently lossy, which is the opposite of
the "every site declares its intent" property we wanted.

## 4. Architecture

```
 client ──► rest_parser / json body
              │  (arbitrary bytes)
              ▼
        NewRequestMethod(name)              ← one scan, no allocation when valid
              │
      ┌───────┴────────┐
      │                │
   Name()        ValidUTF8Name()
      │                │
      │                ├─► prometheus WithLabelValues   (panicked before)
      │                ├─► dimension key → dims.go label (panicked before)
      │                └─► stats key → proto.Marshal      (failed before)
      │                       ▲
      │                       └── observer and RequestResult carry the whole
      │                           RequestMethod; the stats key builder is where
      │                           the valid form is explicitly asked for
      │
      ├─► routing, spec lookups, MethodMatcher, cache keys, rate-limit keys
      ├─► BuildRestURL (upstream URL must be byte-exact)
      └─► logs and client-facing error text
```

## 5. Implementation

**The type** — `internal/protocol/request_method.go`:

```go
type RequestMethod struct {
	name          string
	validUTF8Name string
}

func NewRequestMethod(name string) RequestMethod
func (m RequestMethod) Name() string
func (m RequestMethod) ValidUTF8Name() string
```

Built in every request constructor; the same value is passed to both the
request's own field and the observer, so the two forms are derived exactly once
per request and cannot diverge. `RequestHolder.Method()` returns it — only two
implementations and no mocks, so the interface change is contained.

**The valid form is used at 12 conversion sites** (8 `ValidUTF8Name()`, 4
`utils.ToValidUTF8`), covering 13 Prometheus label arguments and the proto-bound
stats key:

| Site | Failure it prevents |
|---|---|
| `flow/execution_flow.go` — `requestTotalMetric`, `requestErrorsMetric`, `quorumVerificationsMetric` ×2 | `panic` → process death |
| `flow/request_processor.go` — `hedgeMetric` | same |
| `caches/cache_processor.go` — `requestCache` | same |
| `dimensions/dimension_hook.go` — the `GetUpstreamDimensions` call | same, but reached as `key.method` rather than `request.Method()` |
| `ws/registry_commands.go` — the three `jsonRpcWsConnectionsMetric` label arguments | same, from a *different* taint source (see below) |
| `ratelimiter/budget.go` — the `rate_limit_budget_requests` / `_exceeded` labels | same; unreachable today, kept as defence in depth (see below) |
| `stats/base_stats_service.go` — the `StatsKey.Method` assignment | `proto.Marshal` failure dropping a whole `StatsBatch` |

`internal/dimensions` itself needs no change: `GetUpstreamDimensions` has exactly
one non-test caller and `upstreamDimensionKey.method` is only ever a Prometheus
label, so sanitizing at the hook keeps the package's `string` API.

**That sanitization is load-bearing, not a precaution.** `DimensionHook` *does* fire
with a tainted method: `execution_flow.go` calls `reqObserver.AddResult` for every
`*UnaryResponse` — including one carrying `UpstreamId == NoUpstream` — and
`responseReceive` dispatches the hooks unconditionally. A JSON-RPC method with an
invalid byte resolves a *fallback* spec method, so it passes the
`SpecMethod() == nil` gate, then fails `MethodMatcher` and arrives here as a
no-upstream result. Verified live: three tainted names each produced
`nodecore_upstream_requests_total{chain="ethereum",method="eth_taintN�",upstream="NoUpstream"}`.
This is also why §2's cardinality non-goal covers the upstream-side metrics, not
just the two client-facing counters.

`ratelimiter.Allow` is the same shape as the dimension hook: a function that is both
a semantic consumer (rule matching, engine keys) and a label sink
(`rate_limit_budget_requests`, `rate_limit_budget_exceeded`). Its two label
arguments take the valid form; the rules and engine keys keep the raw name. Unlike
the dimension hook this one really is unreachable today — callers gate on
`matched.Type() == SuccessType`, hence `MethodMatcher` against an explicit method
set — so it is defence in depth, one `MethodMatcher` regression away from
resurrecting the crash class otherwise.

The `proto.Marshal` path is fixed at its single sink. `RequestObserver` and
`UnaryRequestResult` both hold the whole `RequestMethod` — nothing along the way
silently narrows it — and `statsdata.StatsKey.Method` has exactly one writer
(`base_stats_service.go`, from `UnaryRequestResult.GetMethod()`), which is where
`ValidUTF8Name()` is asked for. `StatsKey.Method` and the proto field stay plain
strings, so nothing under `internal/stats` or `internal/integration` changes
structurally.

An alternative was to have `WithMethod` store `ValidUTF8Name()` and keep the
result's method a `string`. Rejected: it converts in a setter, so the stats code
would read as if a plain name were being copied, and a future reader could not
tell which form they had. Carrying the struct all the way down means the choice is
visible exactly where it is made.

**The ws `subscription` label is an independent taint source** that the type
cannot cover: `getSubscription` (`ws/ws_protocol.go`) returns `params[0]`'s raw
JSON content for `eth_subscribe` and the method name otherwise, so the value can
be arbitrary client bytes with no method involved. Hence `utils.ToValidUTF8` at
the three label sites, with `subType` itself left raw — which also covers the
`sub.subType` struct-field read, the same shape as the `dims.go` case.

**Everything else takes `Name()`** — 49 of the 59 non-test `Method()` call sites
(the other 10: 7 take `ValidUTF8Name()`, 3 are `RequestOperation.Method()`,
still a plain string). Those 49 are the 22 `%s` args, the zerolog
`Str("method", …)`, and the semantic uses (matchers, `RateLimiterBudget.Allow`,
`getMethodTranslator`, `BanMethod`, cache-policy matching, `BuildRestURL`, the
keydata contract/method checks, ws spec lookups). Notably
`NotSupportedMethodError` and `NoApiConnectorsError` keep the raw form:
reflecting a client's own bytes back in an error body harms nobody, and no crash
or dropped batch results.

`RequestOperation.Method()` (ws) and `UnaryRequestResult.GetMethod()` stay plain
strings — the former is fed `Name()` and is unreachable with a tainted name
anyway (`ws_processor.go` errors out unless a spec method resolved), the latter
comes from the observer and is therefore already valid.

## 6. Edge cases

- **Logs keep the raw bytes on purpose**, and stay valid JSON regardless:
  zerolog's encoder replaces any invalid sequence with `�`
  (`rs/zerolog/internal/json/string.go`). Metrics get the safe form, logs keep
  the truth.
- **`""` as the replacement string was rejected.** It fabricates a plausible
  name — `eth_` + `0x80` + `call` would sanitize to exactly `eth_call`, counting
  garbage under a real method's series — and a fully-invalid name would become an
  empty label, which Prometheus treats as equivalent to the label being absent.
- **Valid non-ASCII is untouched**: `"юникод"` is byte-identical in both forms.
- **go-cmp**: `pkg/test_utils/matchers.go` needs
  `cmpopts.EquateComparable(protocol.RequestMethod{})`, otherwise go-cmp panics
  descending into the new struct's unexported fields.

## 7. Testing

- Unit (`pkg/utils/strings_test.go`, `internal/protocol/request_method_test.go`):
  valid input unchanged in both forms; each repro byte sequence (`%80`, `%FF`,
  `%C3`, `%F0%9F%98`, `%ED%A0%80` surrogate, `%C0%AF` overlong) leaves `Name()`
  byte-identical while `ValidUTF8Name()` passes `utf8.ValidString`; an invalid
  run collapses to one `�`; comparability holds.
- Regression: the label form is accepted by a real `prometheus.CounterVec` **and
  the raw form still panics** — asserting both directions keeps the reason for
  `ValidUTF8Name` visible. Plus the observer hands the valid form to the stats
  result while `Method().Name()` stays byte-exact.
- `rest_request_test.go` template expectations unchanged — the guard proving
  `Name()` is still byte-exact for routing and cache keys.
- End-to-end against a running instance (throwaway config; a stub EVM node so
  the chain was genuinely *available*, otherwise "no available upstreams"
  short-circuits before the crash site):
  - REST `status%80` and `%C0`, JSON-RPC bodies with raw `0x80`, a UTF-16
    surrogate, an overlong encoding, and a mixed batch → error responses,
    **process alive, zero panics**;
  - metric labels contain `ef bf bd` (U+FFFD, verified by hexdump) while error
    bodies contain the raw bytes — exactly the intended split;
  - normal requests on both chains still return data under their own labels.
- `make test` passes except the two Docker-backed `caches/*_e2e` packages, which
  fail identically on a clean `origin/main` worktree in this environment.
  `golangci-lint run ./...` → 0 issues.
