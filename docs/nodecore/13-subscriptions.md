# Subscriptions guide

A subscription is a long-lived stream a client opens to receive events as they happen, rather than
polling for them. nodecore serves subscriptions over WebSocket (`eth_subscribe` and the chain-family
equivalents).

nodecore does not blindly forward each client subscription to an upstream. Two mechanisms sit in
between:

1. **Aggregation** — many identical client subscriptions are collapsed onto a single shared upstream
   source, and events are fanned out to every client.
2. **Local synthesis** — for a handful of EVM topics (`newHeads`, `logs`, `newPendingTransactions`,
   and the synthetic `drpc_pendingTransactions`), nodecore can build the stream itself by aggregating
   data across all upstreams, instead of relaying a single upstream's subscription. This is gated
   per chain by [`local-subscriptions`](#configuration).

## Aggregation

Without aggregation, ten clients subscribing to `newHeads` would open ten upstream WebSocket
subscriptions. Instead, nodecore runs a per-chain **subscription engine** that deduplicates
subscriptions: the first client to ask for a given stream builds a single shared upstream source,
and every subsequent client asking for the *same* stream attaches to that source. One upstream
subscription, N clients.

Each client still gets:

- its own client-facing subscription ID (hex for EVM, numeric for Solana),
- its own subscription confirmation,
- and, where applicable, its own filtering (see [logs](#logs) below).

### What counts as "the same stream"

Subscriptions are deduplicated by an **aggregation key**:

```
key = RequestHash(method + params) | selectorKey(selectors)
```

- `RequestHash` is a deterministic hash of the method and params, so any difference in the requested
  topic or filter object produces a different key.
- `selectorKey` captures routing selectors (the constraints that decide *which* upstreams may serve
  the request). It is order-independent and de-duplicated, so the same selectors in any order share a
  key; different selectors route to a separate source. The match-any wildcard selector is a no-op and
  does not affect the key.

Subscriptions whose keys match share one upstream source; subscriptions whose keys differ get their
own.

### Lifecycle

- The engine is created lazily **per chain** and is shared process-wide, so HTTP/WebSocket clients asking for the same stream are coalesced onto the same source.
- When the **last** subscriber detaches, the source is not torn down immediately — it is kept alive
  for a short grace period (~10s) so a quick re-subscribe reuses it instead of paying to rebuild.
- Terminal state is delivered out-of-band: when a source ends, each subscriber's event channel is
  closed and the terminating error is surfaced to the client.

## Local subscriptions vs node-backed passthrough

For each subscription request, nodecore decides (in `resolveSource()`,
`internal/upstreams/flow/sub_aggregation.go`) whether to serve it from a **locally-synthesized**
source or from a **node-backed passthrough**.

A node-backed passthrough is the generic path: nodecore picks one upstream, opens a single
WebSocket subscription there, and relays its events. It works for any chain and any subscription
method, and it is still aggregated by key (so identical passthrough subscriptions share one upstream
subscription).

Local synthesis replaces that single-upstream relay with a stream nodecore assembles across upstreams.
There are four local source types:

| Topic | How it is synthesized | Capability gate | Config flag |
|---|---|---|---|
| `newHeads` | Taps the chain's merged head stream (the fork-choice winner) and forwards head notifications | `NewHeadsCap` | `enable-new-heads` |
| `logs` | One shared unfiltered log stream built from per-block `eth_getLogs`; per-client address/topic filtering | `LogsCap` | `enable-logs` |
| `newPendingTransactions` | Merges the `newPendingTransactions` feeds from every WebSocket upstream and de-duplicates hashes | `PendingTxCap` | `enable-new-pending-transactions` |
| `drpc_pendingTransactions` | Reuses the shared pending-hash source and enriches each hash via `eth_getTransactionByHash` | `PendingTxCap` | *always local (ungated)* |

**Fallback rule.** nodecore falls back to a node-backed passthrough when any of these hold:

- the topic's `local-subscriptions` flag is turned off,
- no upstream on the chain has the required capability, or
- (for `logs` only) the request carries effective routing selectors — a selector-constrained logs
  subscription cannot be served from the shared all-upstream log stream, so it goes to a single
  upstream.

`drpc_pendingTransactions` is the exception: it is a synthetic method with no node-backed
equivalent, so it is always served locally (subject only to an upstream having `PendingTxCap`).

## Per-type behavior

### newHeads

There is one merged head per chain, so the local `newHeads` source is **one source per chain** and
ignores request selectors. It taps the chain's head stream and forwards the upstream head
notification payload verbatim.

### logs

nodecore maintains a single **unfiltered "all logs"** source per chain: for each new block it fetches
that block's logs (`eth_getLogs` by block hash, against any upstream at or above the block height)
and emits them. Each client's `address`/`topics` filter from its `eth_subscribe("logs", {...})`
request is then applied **locally**, so every client sees only its matching logs while still sharing
the one upstream source.

- **Reorgs**: recent blocks' logs are cached. When a block is dropped by a reorg, its cached logs are
  re-emitted with `"removed": true`, matching standard `eth_subscribe("logs")` semantics. Reorgs
  deeper than the bounded history window are clamped (tracked by a metric).
- **Selectors bypass local logs**: as noted in the [fallback rule](#local-subscriptions-vs-node-backed-passthrough),
  a logs request with effective selectors uses a node-backed passthrough instead.
- A block whose logs cannot be fetched is skipped (counted, not fatal); the source ends only when no
  upstream retains the `logs` capability.

### newPendingTransactions

The local source opens `newPendingTransactions` on **every** WebSocket-capable upstream and merges the
feeds, de-duplicating by transaction hash (an LRU window) so a client sees each pending hash once even
when several upstreams report it. A single dead upstream feed only ends its own feed; the merged
source stays alive on the survivors and terminates only when all feeds die or the capability is lost.

### drpc_pendingTransactions

A synthetic, drpc-specific method with no node-backed equivalent — it is always local. It subscribes
to the same shared pending-hash source as `newPendingTransactions`, then **enriches** each hash by
calling `eth_getTransactionByHash` across available upstreams (first non-null wins) and emits the full
transaction object. Hashes whose transaction has already been mined or dropped resolve to null and are
skipped — this is normal, not an error.

## Configuration

Local synthesis is controlled per chain under
[`upstream-config.chain-defaults.<chain>.local-subscriptions`](05-upstream-config.md#chain-defaults):

```yaml
upstream-config:
  chain-defaults:
    ethereum:
      local-subscriptions:
        enable: true
        enable-new-heads: true
        enable-logs: true
        enable-new-pending-transactions: true
```

Fields:

- `enable` — master switch for the chain. `enable: false` turns off local synthesis for all three
  configurable topics. **_Default_**: `true`
- `enable-new-heads` / `enable-logs` / `enable-new-pending-transactions` — per-topic overrides. Each
  **_defaults_** to the value of `enable` (so `true` unless `enable` is set to `false`).

**Precedence**: a per-topic flag wins over the master `enable`, which wins over the built-in default
of `true`. So you can disable everything except one topic:

```yaml
local-subscriptions:
  enable: false        # off for newHeads and newPendingTransactions
  enable-logs: true    # but keep logs local
```

Notes:

- Settings are **per chain** only — there is no global or per-upstream override.
- They only take effect where the chain actually has the capability; otherwise the topic falls back to
  a node-backed passthrough regardless of the flag.
- `drpc_pendingTransactions` is **never** gated by these flags — it is always served locally.
- Defaults preserve the historical behavior of always synthesizing locally when possible.

## Termination and errors

Subscriptions end out-of-band: the client's event channel closes and the terminating error (for
example, total WebSocket failure when an upstream subscription is lost) is surfaced. A client that
cannot keep up with its event stream is disconnected rather than having events silently dropped, so a
slow consumer never receives a gap-riddled stream that looks complete.

## Metrics

Subscription activity is exposed on the metrics port (see [Prometheus metrics](08-prometheus-metrics.md)):

- [WebSocket Metrics](08-prometheus-metrics.md#websocket-metrics) — connection and upstream
  subscription/operation counters.
- [Subscription Utilities Metrics](08-prometheus-metrics.md#subscription-utilities-metrics) — event
  rate, active subscription count, and unread/backpressure gauges for the aggregation channels.
- [Logs Subscription Metrics](08-prometheus-metrics.md#logs-subscription-metrics) — the local logs
  source counters `nodecore_logs_source_blocks_skipped_total` (by reason) and
  `nodecore_logs_source_reorg_clamped_total`.

## See also

- [Upstream config — `chain-defaults`](05-upstream-config.md#chain-defaults) — the canonical
  `local-subscriptions` schema reference.
- [Method specs](11-method-specs.md#settings) — the `subscription` (`is-subscribe`,
  `unsubscribe-method`) and `group: "sub"` fields that mark a method as a subscription.
