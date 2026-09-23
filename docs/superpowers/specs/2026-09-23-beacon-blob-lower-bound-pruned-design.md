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
~4096 epochs below head for a regular node, the first blob-carrying slot after
the Deneb fork for an archive or supernode.

Non-goals (deliberately minimal):
- no per-client special-casing, retention arithmetic or extra requests per probe;
- no change to the search calculator, the other beacon detectors or the
  published bound types.

## 3. Key decisions

| decision | outcome |
|---|---|
| What counts as "blobs available" | only a non-empty sidecar list; `200 {"data":[]}` is a miss |
| Blob-less slots inside the retention window | stepped over by the existing offset search (`shiftLeftAndSearch`) |
| Offset for the blob detector | `blobBoundMaxOffset = 32` (one epoch), other beacon detectors keep 20 |
| `insufficient data columns` | added to the blob not-found hints: a miss, not a retried probe error |
| Resolving an empty answer against the block | rejected: multiplies requests with the offset scan and depends on block retention |

## 4. Terminology

- **sidecars** — `blob_sidecars` answer for a slot.
- **retention window** — slots whose blobs the node still serves.
- **blob-less run** — consecutive slots without blobs (no blob txs or no block).

## 5. Architecture

```
calculator (binary search + offset 32)
  probe(slot) -> blob_sidecars(slot)
     non-empty data          -> available
     200 {"data":[]}         -> miss
     404 / not-found hint    -> miss   (pre-deneb, insufficient data columns, ...)
     other error             -> probe error (retried)
  miss at middle -> scan up to 32 slots below for any available slot
     found     -> keep searching left (inside the window, blob-less run)
     not found -> move right (pruned)
```

## 6. Probe design

`newBlobDetector` uses `hasNonEmptyData` instead of `hasDataKey` and
`blobBoundMaxOffset` instead of `beaconBoundMaxOffset`. `hasNonEmptyData`
decodes the raw `data` array because sonic's lazy `ast.Node.Len` reports 0 for
an unloaded array.

## 7. Edge cases

- **Blob-less run longer than 32 slots inside the window:** the search treats
  it as pruned and the bound lands above it. Measured below on mainnet.
- **Archive:** the bound is the first blob-carrying slot after the fork instead
  of the fork slot itself; slots in between have no blobs to serve.
- **Cached bound:** the calculator re-checks the cached bound first; an old
  Deneb-fork bound now fails that check and triggers a full search. Between
  searches the bound is predicted from block time, so search latency is not an
  issue.

## 8. Testing

Unit tests in `beacon_lower_bound_test.go`:
- pruned window answering `200 data:[]`, blob-less odd slots inside the window
  → bound lands on the retention window (the old code returns the Deneb fork);
- pruned window answering `400 Insufficient data columns` → same bound;
- archive with blob-less slots interleaved → bound on the first blob slot after
  the fork;
- the existing pre-Deneb test, now with non-empty sidecars in the retained range.

## 9. Real-data validation

Old and new binaries against three mainnet Lighthouse v8.2.2 nodes and one blob
archive exposing the beacon blob API:

| upstream | old BLOB bound | new BLOB bound |
|---|---|---|
| Lighthouse ×3 (default pruning) | 8,626,176 | 15,147,552 |
| Lighthouse supernode, `--prune-blobs false`, incomplete backfill | 8,626,176 | 9,430,752 |
| blob archive | 8,626,176 | 8,626,498 |

With head at 15,278,722 the pruning Lighthouse bound is 4096 epochs plus the
head movement during the run; 8,626,498 is the first blob-carrying slot after
the Deneb fork.

The supernode had never backfilled its earliest blobs: sampled slots answer
`404 no blobs stored` up to ~9.32M and serve blobs from ~9.46M on, with one
more hole around 12.37M. The new bound is exact (slot 9,430,751 → `404 no blobs
stored`, 9,430,752 → sidecars); the hole did not derail the search. The old
bound advertised ~800k slots of blobs the node does not have.

## 10. Key code references

- `internal/upstreams/lower_bounds/beacon_bounds/beacon_lower_bound.go` —
  `newBlobDetector`, `hasNonEmptyData`, `blobBoundMaxOffset`.
- `internal/upstreams/lower_bounds/lower_bound_search.go` —
  `detectWithOffset`, `shiftLeftAndSearch` (unchanged).

## 11. Open questions / future

- Prysm, Teku, Nimbus and Lodestar pruning answers were not checked on live
  nodes; they are covered if they answer `404`, a known hint, or an empty list.
- If blob usage drops and blob-less runs longer than one epoch become common,
  `blobBoundMaxOffset` has to grow with them.
