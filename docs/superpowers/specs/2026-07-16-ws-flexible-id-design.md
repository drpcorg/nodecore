# Tolerant JSON-RPC id in the ws message parser — design

- **Date:** 2026-07-16
- **Status:** Implemented — branch `fix/ethermint-ws-heads`
- **Area:** `internal/protocol` (`parse_response.go`)

## 1. Problem

nodecore sends ws JSON-RPC requests with a **string** id (`{"id":"1"}`) and
parsed responses with a strict `Id string` field. Per JSON-RPC 2.0 the id is
a string, a number or null — and some upstream nodes reply with a **numeric**
id even when the request carried a string one. Ethermint/evmos-lineage nodes
(cosmos-EVM chains such as zeta-chain and mezo) do exactly that, but only on
the `eth_subscribe` path:

```
>> {"id":"1","jsonrpc":"2.0","method":"eth_subscribe","params":["newHeads"]}
<< {"jsonrpc":"2.0","result":"0xad3ddeb5ed159eb69129a94e1b5b8a1c","id":1}
```

(captured with a logging ws proxy between nodecore and a live node; the same
node echoes string ids verbatim on regular calls like `eth_blockNumber`).

`sonic.Unmarshal` fails on `"id":1` vs `Id string`, the whole frame is
classified `Unknown`, `ParseWsMessage` returns an error — and the ws
processor **tears down the entire connection** and reconnects. The result on
every ethermint upstream:

- a permanent connect → subscribe → parse-fail → disconnect loop
  (visible as constant TIME_WAIT churn towards the node's ws port);
- `newHeads` notifications are never consumed; heads come only from the
  `getLatestBlock` fallback at each (re)subscribe, so the reported head is
  **~60s / 10-20 blocks stale**;
- the head hash source silently diverges from sibling provider instances
  subscribed to the same node (they use the notification hashes), so the
  consumers' fork detection flags the nodecore-fed connections as forked and
  pulls them from rotation. Two production chains sat in that state for days.

## 2. Goal

Accept every id shape JSON-RPC 2.0 allows, so a spec-legal response can never
kill the connection; ethermint upstreams get working `newHeads` subscriptions
with per-block head freshness and the same head-hash source as other
subscribers of the same node.

### Non-goals

- **Choosing the head-hash source** (notification hash vs re-fetched
  canonical hash) — see §8; deliberate follow-up, not this change.
- Changing the outgoing request id format (string ids stay).
- HTTP response parsing (ids there are echoed by the same code path that
  produced them; no observed issue).

## 3. Key decisions (settled → implemented)

| # | Decision | Choice |
|---|---|---|
| 1 | Where to fix | The ws frame parser only (`jsonRpcWsMessage`), the single place that misclassified frames |
| 2 | Id representation | `flexibleId` string type with custom `UnmarshalJSON`: strings decode normally, numbers keep their raw decimal text, `null`/absent → empty |
| 3 | Number normalization | Raw text form (`1` → `"1"`) — exactly what the request registry keys on, since outgoing ids are `fmt.Sprintf("%d", n)` |
| 4 | Non-integer ids | Kept as raw text (`1.5` → `"1.5"`); they can never match a registry key and are ignored like any unknown id — no special casing |
| 5 | Failure mode | Unparseable id still fails the frame (unchanged); only the legal-but-numeric case is new |

## 4. Architecture

```
 node ws ──► ws_session ──► ParseWsMessage ──► requestRegistry (match by id / subId)
                              │
                              ├─ before: "id":1 → unmarshal error → Unknown → DISCONNECT ⚠
                              └─ after:  "id":1 → flexibleId "1" → JsonRpc → matched, sub registered
```

## 5. Implementation

`internal/protocol/parse_response.go`: `jsonRpcWsMessage.Id` becomes
`flexibleId`; `ParseJsonRpcWsMessage` reads `string(wsMessage.Id)`. No other
call sites — the type is private to the parser.

## 6. Edge cases

- `"id":null` → empty id; for a frame with neither id, subId nor error the
  existing "wrong json-rpc ws response" classification still applies.
- Huge numeric ids: kept as raw text, no integer overflow possible.
- Frames with both `params` (subscription) and an id are classified as
  subscription events, as before — id handling only affects the RPC branch.

## 7. Testing

- Unit: numeric-id result frame, numeric-id error frame, null id
  (`parse_response_flexid_test.go`) — the first two fail on the old parser.
- Live, against two production ethermint nodes (zeta-chain, mezo):
  - before: 1-2 heads/min, `couldn't parse ws message: invalid response
    type - unknown` on every subscribe, constant reconnects;
  - after: **18 heads/min (zeta, ~3.5s block time) and 14 heads/45s (mezo)**,
    zero parse errors, zero resubscribes over the observation window;
  - head hashes match the raw `newHeads` notification stream **21/21** —
    i.e. parity with every other ws subscriber of the same node.

## 8. Open questions / future

- **Ethermint notification hashes are not the canonical block hashes** (the
  node's own `eth_getBlockByNumber` disagrees with its `newHeads` `hash`
  field, while `parentHash` in notifications is canonical). With this fix
  nodecore reports the notification hashes — consistent with other
  subscribers of the same node, which is what consumers' quorum-based fork
  detection needs. Moving the fleet to canonical hashes (fetch-after-notify
  or verify-then-trust) is a separate, coordinated change: a single provider
  switching alone becomes the minority voice and gets flagged as forked —
  exactly the incident that surfaced this bug.
- Consider reporting the notification-vs-canonical hash mismatch upstream.
  The original evmos/ethermint repo is archived (April 2024); the living
  successor is **cosmos/evm**, and affected chains vendor their own forks —
  those are the actionable places for a report.
