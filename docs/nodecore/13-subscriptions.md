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
| `newHeads` | Follows a head feed over the available upstreams with a subscription-driven head that match the request's selectors, and forwards their head notifications | `NewHeadsCap` | `enable-new-heads` |
| `logs` | One shared unfiltered log stream per selector set, built from per-block `eth_getLogs` on a head feed filtered to upstreams with `LogsCap`; per-client address/topic filtering | `LogsCap` | `enable-logs` |
| `newPendingTransactions` | Merges the `newPendingTransactions` feeds from every WebSocket upstream and de-duplicates hashes | `PendingTxCap` | `enable-new-pending-transactions` |
| `drpc_pendingTransactions` | Reuses the shared pending-hash source and enriches each hash via `eth_getTransactionByHash` | `PendingTxCap` | *always local (ungated)* |

**Fallback rule.** For `newHeads` and `logs`, nodecore uses a node-backed passthrough only when the
topic's `local-subscriptions` flag is turned off. With the flag on (the default) the subscription is
served locally or not at all: when no available upstream advertises the capability **and** matches
the request's selectors right now - the chain's heads are all polled, every capable upstream is down,
or the selectors exclude them all - the subscription fails with `no available upstreams` rather than
being silently rerouted to a single node the client did not select. A chain that cannot serve a
topic locally (for example an EVM chain whose upstreams all have a JSON-RPC head connector) must
have that topic's flag turned off.

`newPendingTransactions` still falls back to a passthrough when no upstream has `PendingTxCap`.

`drpc_pendingTransactions` is the exception: it is a synthetic method with no node-backed
equivalent, so it is always served locally (subject only to an upstream having `PendingTxCap`).

### Head-liveness gate on `WsCap`

Before an upstream can be a subscription target at all (local *or* node-backed), it must advertise
the base WebSocket capability `WsCap`. For an EVM upstream whose head is driven by a WebSocket,
`WsCap` is additionally gated on **head liveness**: the upstream advertises it only once its head has
advanced consecutively — 3 blocks in a row (2 consecutive height increments) — and drops it on a
forward gap of skipped blocks, a stall (no head progress within an adaptive timeout), or a WebSocket
disconnect; after a gap it stays out for a short cooldown before recovering (duplicate heights and
backward reorgs are tolerated). A flapping head therefore removes that upstream from subscription
serving until it recovers, while leaving its regular RPC routing untouched. Poll-head EVM upstreams
and non-EVM chains keep the plain "connected WebSocket ⇒ `WsCap`" behavior. The gate can be turned
off per chain/upstream with [`disable-liveness-subscription-validation`](05-upstream-config.md)
(default: mode-dependent — on in `strict` mode, off in `default` mode).

## Per-type behavior

### newHeads

The local `newHeads` source follows a **filtered head feed**: the chain supervisor computes a head
over only the available upstreams whose head is subscription-driven (`NewHeadsCap`) and that match
the request's selectors, re-evaluated as upstreams come and go, lose capabilities or change status.
The source forwards each head's upstream notification payload verbatim. The client whose
subscription creates the source also receives the current head first; clients that join a source
already running for the same selectors start at the next head. Clients with identical selectors
share one source; different selectors get different sources (sort hints such as `latest` do not
count, since they order candidates rather than filter them).
The source ends with a terminal error when no upstream passes the filter any more; the client
resubscribes, and the new subscription is resolved afresh.

### logs

nodecore maintains a single **unfiltered "all logs"** source per chain and selector set: it follows
a filtered head feed over the available upstreams that can serve logs (`LogsCap`, i.e. a
subscription-driven head plus `eth_getLogs`) and match the request's selectors, and for each new
block fetches that block's logs (`eth_getLogs` by block hash, against any upstream at or above the
block height) and emits them. Each client's `address`/`topics` filter from its
`eth_subscribe("logs", {...})` request is then applied **locally**, so every client sees only its
matching logs while still sharing the one upstream source.

- **Reorgs**: recent blocks' logs are cached. When a block is dropped by a reorg, its cached logs are
  re-emitted with `"removed": true`, matching standard `eth_subscribe("logs")` semantics. Reorgs
  deeper than the bounded history window are clamped (tracked by a metric).
- **Head of the logs stream**: blocks are announced from the head feed, so a block is never
  announced before an upstream that can serve its logs has it. An upstream without `eth_getLogs`
  that runs ahead does not announce blocks; the logs stream follows the capable upstreams instead.
  Membership is re-evaluated on every upstream event (method ban, capability loss, status change,
  removal), and the first announced block is the feed's current head.
- **Backfill**: heights that head jumps over, and the new chain after a reorg, are fetched by parent
  hash (`eth_getBlockByHash`, up to 32 blocks back) and announced in order. A deeper gap, or a failed
  fetch, announces the head with the gap.
- **Not-ready blocks**: an upstream may announce a block before it can serve its logs - erigon
  dispatches `newHeads` before it commits the block, cosmos-evm nodes index the block hash after the
  header event. An answer such as `block range extends beyond current head block`, `block not
  found`, `unknown block` or `header not found` moves on to the next upstream, as any error does; when
  no upstream at the height could serve the block, they are asked again with a backoff (100 ms to
  1 s) until one block time after the head arrived (clamped to 1–3 s). Other errors are not waited on:
  an upstream that answered one is not asked again for that block.
- A block whose logs cannot be fetched is skipped (counted, not fatal); the source ends only when no
  upstream passes the feed's filter any more.

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

> **⚠️ `drpc_pendingTransactions` ignores `enable-new-pending-transactions`.** Because it reuses the
> shared pending-hash source, a client subscribing to `drpc_pendingTransactions` opens
> `newPendingTransactions` on **every** WebSocket-capable upstream — exactly the load you may have
> intended to avoid by setting `enable-new-pending-transactions: false`. That flag only gates the
> client-facing `newPendingTransactions` topic; it does **not** stop `drpc_pendingTransactions` from
> tapping the mempool. If your goal is to eliminate mempool tapping entirely, you must also ensure no
> client subscribes to `drpc_pendingTransactions` (e.g. forbid the method via access-key scoping).

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
- With a flag on, `newHeads` and `logs` are served locally or fail with `no available upstreams`;
  turn the flag off for a chain that cannot serve the topic locally. `newPendingTransactions` still
  falls back to a node-backed passthrough where no upstream has the capability.
- `drpc_pendingTransactions` is **never** gated by these flags — it is always served locally, and it
  still taps the mempool on every WebSocket upstream even when `enable-new-pending-transactions: false`
  (see the warning under [drpc_pendingTransactions](#drpc_pendingtransactions)).
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
  source counters `nodecore_logs_source_blocks_skipped_total` (by reason),
  `nodecore_logs_source_backfill_failed_total` and `nodecore_logs_source_reorg_clamped_total`.

## See also

- [Upstream config — `chain-defaults`](05-upstream-config.md#chain-defaults) — the canonical
  `local-subscriptions` schema reference.
- [Method specs](https://github.com/drpcorg/public/blob/main/docs/method-specs.md#settings) — the `subscription` (`is-subscribe`,
  `unsubscribe-method`) and `group: "sub"` fields that mark a method as a subscription.
