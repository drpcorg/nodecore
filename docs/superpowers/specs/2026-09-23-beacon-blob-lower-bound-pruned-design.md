# Beacon blob lower bound on pruning nodes — design

- **Date:** 2026-09-23 (implemented 2026-09-23)
- **Status:** implemented on branch `arootman/beacon-blob-bound-pruned`
- **Area:** `internal/upstreams/lower_bounds/beacon_bounds`

## 1. Problem

The beacon blob detector probes `GET /eth/v1/beacon/blob_sidecars/{slot}` and
counts any `200` answer carrying a `data` key as "blobs available", including an
empty list. The assumption was that a pruned slot answers `404`.

Lighthouse does not do that. After pruning it keeps answering
`200 {"data":[]}` for Deneb/Electra slots, exactly like a block that never
carried blobs. For Fulu slots it no longer holds enough data columns for it
answers `400 "Insufficient data columns to reconstruct blobs: required 64, but
only 0 were found"`, which matches no not-found hint and is treated as a probe
error (retried with backoff, then counted as a miss).

Observed on mainnet (Lighthouse v8.2.2, default custody, head ≈ 15,277,300):

| slot | era | answer |
|---|---|---|
| 8,626,200 | Deneb, pruned | `200 {"version":"deneb","data":[]}` |
| 15,132,606 | Fulu, ~20 days old | `400 Insufficient data columns ...` |
| 15,276,000 | Fulu, recent | `200` with sidecars |

The binary search probes old Deneb slots, sees `200 data:[]`, and converges on
the Deneb fork slot (8,626,176). Every such node therefore advertised the same
blob bound as a full blob archive. A router relying on the bound sent requests
for 18+ day old blobs to these nodes, which failed with `400`; clients that
need old blobs (rollup nodes deriving from L1 batches) slowed down to the
retry rate.

## 2. Goal

Publish a blob lower bound that matches what the node can actually serve:
~4096 epochs below head for a regular node, the Deneb fork for an archive or
supernode.

Non-goals (deliberately minimal):
- no per-client special-casing or retention arithmetic;
- no change to the search calculator, the other beacon detectors or the
  published bound types.

## 3. Key decisions

| decision | outcome |
|---|---|
| Resolve an empty sidecar list against the block | implemented: blob commitments without sidecars = pruned (miss) |
| Block without commitments | inconclusive: scan forward to the next block that carried blobs |
| Scan window | one epoch (32 slots); no blob-carrying block in the window keeps the old "available" answer |
| `insufficient data columns` | added to the blob not-found hints: a miss, not a probe error |
| Block endpoint | `GET /eth/v2/beacon/blocks/{slot}`, already used by the block detector |

## 4. Terminology

- **sidecars** — `blob_sidecars` answer for a slot.
- **commitments** — `data.message.body.blob_kzg_commitments` of the block at a slot.
- **retention window** — slots whose blobs the node still serves.

## 5. Architecture

```
probeBlobs(slot)
  for s in [slot, slot+32):
    sidecars(s) ──not found──> s == slot ? miss
                               : block(s) present ? miss : continue (skipped slot)
    sidecars(s) non-empty ───> available
    sidecars(s) empty ───────> block(s) has commitments ? miss (pruned)
                               : continue (no blobs in this block)
  window exhausted ─────────> available (nothing contradicts the 200s)
```

## 6. Probe design

`beaconProber.fetch` is split out of `doProbe` so the blob probe can inspect
the body of a found answer; `doProbe` keeps its behaviour for the block, state
and epoch detectors.

`jsonArrayLen` decodes the raw array instead of calling `ast.Node.Len`, which
reports 0 for a lazily-parsed sonic array.

## 7. Edge cases

- **Skipped slot inside the scan:** sidecars not found and no block → continue.
- **Probed slot itself not found:** miss, as before (the offset search already
  shifts left over sporadic missing slots).
- **Near head:** future slots are not found and have no block, the scan runs out
  and returns available; only reachable when the whole node is fresh.
- **Archive / supernode:** blob-carrying blocks always return sidecars, so the
  bound stays on the Deneb fork.
- **Cached bound:** the calculator re-checks the cached bound first; an old
  Deneb-fork bound now fails that check and triggers a full search.

## 8. Testing

Unit tests in `beacon_lower_bound_test.go`:
- pruned window answering `200 data:[]` with commitments in the block → bound
  lands on the retention window (fails on the old code: 20 instead of 60);
- pruned window answering `400 Insufficient data columns` → same bound;
- archive with blob-less blocks interleaved → bound stays on the Deneb fork;
- the existing pre-Deneb test, now with non-empty sidecars in the retained range.

## 9. Real-data validation

Old and new binaries side by side against three mainnet Lighthouse v8.2.2 nodes
and one blob archive exposing the beacon blob API:

| upstream | old BLOB bound | new BLOB bound |
|---|---|---|
| Lighthouse ×3 | 8,626,176 | 15,146,207 |
| blob archive | 8,626,176 | 8,626,176 |

15,146,207 is 131,093 slots below head, i.e. the 4096-epoch blob retention.
Spot check on one node: slot 15,146,100 → `400 Insufficient data columns`,
15,146,207 → `200 data:[]` (block without blobs), 15,146,300 → sidecars.

## 10. Key code references

- `internal/upstreams/lower_bounds/beacon_bounds/beacon_lower_bound.go` —
  `probeBlobs`, `blockBlobCommitments`, `fetch`, `jsonArrayLen`,
  `blobNotFoundHints`.
- `internal/upstreams/lower_bounds/lower_bound_search.go` — calculator (unchanged).

## 11. Open questions / future

- Prysm, Teku, Nimbus and Lodestar pruning answers were not checked on live
  nodes; they are covered only if they answer `404`, a known hint, or an empty
  list for a block with commitments.
- An empty answer costs one extra block request per probed slot (plus the
  forward scan on blob-less blocks), every 5 minutes per upstream.
