# Node group status performance — 2026-09-30

## Scope and reproducibility

Local measurements on Apple M4 Pro, darwin/arm64, Go 1.27.1, default
GOMAXPROCS=14. Baseline: nodecore `87aead601455e384e52cb66a80e278b85244466d`.
Benchmarks use real method sets, group computation and protobuf serialization;
only supervisor reads are fixtures. Results below are medians of three runs.
CPU profiles were also collected. Benchmark time is wall time, not CPU percent.

```sh
GOWORK=off go run ./cmd/chains
GOWORK=off go test ./internal/server/emerald -run '^$' \
  -bench BenchmarkNodeGroupStatus -benchtime=500ms -count=3
GOWORK=off go test ./internal/server/emerald -run '^$' \
  -bench 'BenchmarkNodeGroup(HeadBurst|ColdSubscriber)' -benchtime=500ms -count=3
NODE_GROUP_LOAD_COMPACT=1 GOWORK=off go test -tags=nodegroups_load \
  ./internal/server/emerald -run '^TestNodeGroupStatusLoad$' -count=3 -v
```

Unset `NODE_GROUP_LOAD_COMPACT` to measure the backward-compatible full-description
mode. For the baseline, use a separate checkout at the hash above, copy only
`node_groups_benchmark_test.go` and `node_groups_load_test.go` into its emerald
package, generate chains and run without compact mode. Do not benchmark the two
checkouts concurrently. Standalone consumers pin the published public API.

## Repeated head updates

One operation changes one upstream head and serially prepares/serializes responses
for eight subscribers. The large fixtures contain the default 65 call methods
plus 200 synthetic methods. Group keys are already cached; initial subscription
construction is excluded here. Both columns use the original full-description
wire format, isolating the benefit of computation reuse.

| Nodes / mode | Before ms/update | After ms/update | Before allocated MiB/update | After allocated MiB/update |
|---|---:|---:|---:|---:|
| 8 / 2 groups, default methods | 0.647 | 0.106 | 0.862 | 0.116 |
| 32 / 4 groups, 265 methods | 6.138 | 0.340 | 10.964 | 0.344 |
| 32 / singleton, 265 methods | 13.427 | 0.539 | 14.870 | 0.831 |
| 128 / 8 groups, 265 methods | 21.823 | 0.683 | 42.882 | 0.946 |
| 128 / singleton, 265 methods | 53.238 | 1.321 | 58.992 | 2.620 |

The last case reduces repeated-update time about 40x and allocations about 22.5x.
When all 128 singleton heads change together, eight full-description responses
still cost about 6.21 ms and contain 5,225,296 protobuf bytes in total. A new
subscriber's initial singleton catalog costs about 5.25 ms / 6.92 MiB allocated,
and contains 653,180 payload bytes. Cold subscription still requires full work.

## Real TCP/gRPC load

`TestNodeGroupStatusLoad` runs the production status producer over localhost TCP
with eight independent gRPC streams, 128 singleton upstreams and 265 methods.
All upstream heads advance at a target 40 Hz for 200 updates. The producer uses
the production 25 ms throttle. The load is followed by a bounded convergence
check: all 128 heads at all eight clients must reach the final height.

Initial snapshots are awaited before measurement. Periodic resync is excluded
from this short probe and measured separately. Intermediate snapshots may be
coalesced; this is status-stream behavior, not lost final state. Latency measures
when a client has received every group's advertised height for an issued update.

| Median across 3 runs | Baseline | Cached, full descriptions | Cached + compact updates |
|---|---:|---:|---:|
| Measurement duration, s | 5.041 | 5.031 | 5.021 |
| Process CPU time, s | 15.483 | 6.758 | 1.613 |
| Allocated MiB | 11,276.4 | 3,685.1 | 659.4 |
| Protobuf payload MiB | 742.9 | 935.8 | 12.4 |
| Responses received, all 8 streams | 1,194 | 1,504 | 1,529 |
| p95 convergence latency, ms | 34.97 | 29.52 | 25.00 |

CPU time includes both server and clients, including client decoding and GC. It
can exceed elapsed time because the process uses multiple cores. Allocated bytes
are cumulative allocation, not RSS or retained heap. Payload excludes TCP/HTTP2,
TLS and compression. Faster full-description processing delivers more intermediate
snapshots, so its total payload increases even though response sizes do not.

Compact mode reduces measured whole-process CPU about 9.6x and payload about 60x
relative to baseline while delivering more intermediate snapshots. These are
local fixture results, not production capacity or RPC throughput guarantees.

## Aggregator → dproxy

The adapter now forwards compact head/status events using the existing downstream
format. Capability/membership changes and periodic full resync still carry the
complete catalog. The production server mapper benchmark in dproxy is:

```sh
GOWORK=off go test ./services/aggregator/src/server -run '^$' \
  -bench BenchmarkNodeGroupPayload -benchtime=500ms -count=3
```

With 200 methods per group, payload per subscriber changes from 128,152 to 768
bytes for 32 groups, and from 501,688 to 2,976 bytes for 128 groups. This benchmark
measures the mapper/serializer, not the entire reducer or network stack.

## What changed and remaining limits

Nodecore reuses immutable group metadata across head-only state copies and skips
full-description comparisons for unchanged groups. State publication, membership
changes and mode changes invalidate the relevant cache. No shared global group
cache or additional event queue was introduced. A 1,000-move regression checks
that retired groups are not retained in the catalog.

`compact_updates` is opt-in on `SubscribeNodeGroupStatus`. Only unchanged group
metadata can be omitted; initial snapshots, metadata changes and full resync stay
complete. The network description remains complete. Default API behavior is
unchanged. Aggregator opts in and keeps prior metadata on compact group updates;
unknown groups without metadata remain unroutable until a full description arrives.

Race regressions compare cached output against fresh computation after head,
status, bounds, method, label, runtime-index, membership and mode changes. The
cross-repository network suite covers routing/retry/sticky/lifecycle/subscriptions.
The load probe also runs separately with the race detector; performance numbers
come from non-race runs only.

Real Geth/Erigon, production chain distributions, TLS/WAN backpressure, native RPC
traffic competing with discovery, public ingress/auth/billing and sustained
staging soak tests remain unmeasured. A burst of new subscriptions or method/label
churn still requires full descriptions. There is no claim of a production-wide
CPU or bandwidth limit from this local test.
