# Proof lower-bound detection design

Describes what ships in `feat/proofs-sync-status` (PR #357). Supersedes the plan
`docs/superpowers/plans/2026-09-02-proofs-sync-status.md`, which was deleted: it specified an
upper bound type (`LOWER_BOUND_PROOF_UPPER`), a memoizing sync-status source and a gRPC mapping,
none of which shipped.

## Goal

Publish `LOWER_BOUND_PROOF` for EVM upstreams without paying an `eth_getProof` binary search when
the node can answer in one call, and mark such nodes with the label `historical_proofs`.

## Why a separate detector

`eth_capabilities` is unreliable for proofs. op-reth on op-sepolia reports
`stateproofs.oldestBlock == head.number` while `--proofs-history` serves a window 129600 blocks
deep. Ethereum geth reports a usable value. Trusting the report unconditionally would publish the
head as the proof bound and exclude the upstream from every historical proof request.

`ProofBound` therefore left `EvmLowerBoundDetector` entirely. That detector handles block, state,
tx and receipts, and its order stays `eth_capabilities` -> gold bound -> binary search.

## Detector

`internal/upstreams/lower_bounds/evm_bounds/proof_bound.go`

```go
type EvmProofLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator
	evmRpcClient
	capabilities *EvmCapabilities
}
```

- `MainBoundType` and `SupportedTypes()` are `ProofBound` only. Period: `evmLowerBoundPeriod`
  (3 minutes). Search offset 0.
- `evmRpcClient` (`evm_lower_bound.go`) is the JSON-RPC helper shared with `EvmLowerBoundDetector`:
  one call under the internal timeout, no-data errors and null results mapped to "not available".
- `WithCapabilities` attaches the per-upstream `eth_capabilities` cache. Nil is valid: the detector
  then goes from the sync status straight to the search.

### Stage order, every cycle

1. **`debug_proofsSyncStatus`** — the op-reth historical proof store. On a usable window the bound
   is `earliest`; `earliest == 0` is coerced to 1, because a 0 bound reads as "unknown" to routing.
2. **`eth_capabilities`** — `stateproofs` from the shared snapshot.
3. **`eth_getProof` binary search** — `LowerBoundSearchCalculator.DetectLowerBound` with
   `fetchLatestHeight` and `hasProof` (`eth_getProof` at the zero address with an empty key list).

Nothing is memoized. Each cycle is request, parse, result or fall-through. An upstream without
`debug_proofsSyncStatus` pays one rejected request per 3 minutes, which is cheaper than a cached
verdict with its own re-probe timer and mutex. This replaced the deleted `EvmProofsSyncStatus`
(reviewer request on PR #357).

### Stage 1 fall-through conditions

Any of these moves to stage 2 with a debug log: transport or RPC error, including method-not-found;
`available == false` from `call`, which covers a null result; unparseable body; missing `earliest`
or `latest`; `latest <= 0`; `earliest > latest` (a store still initialising); `earliest < 0`.

Both wire shapes are accepted through `parseEvmBlockNumber`: quoted hex (`"0x1a"`) and bare decimal
(`26`). Live nodes answer decimal, which a hex-only parser reads as `0x26`.

### Stage 2 rules

`evmCapabilitiesSnapshot` carries `head` (`head.number` of the same report, 0 when absent or
unparseable) next to `resources`.

| Snapshot state | Result |
| --- | --- |
| no snapshot, or `stateproofs` absent | fall through to the search |
| `stateproofs.disabled == true` | publish nothing (`[]protocol.LowerBoundData{}`), stage terminates. Routing treats an absent bound as "no data of that type" and excludes the upstream |
| `head == 0` or `bound >= head` | fall through to the search, with a debug log naming both values |
| `bound < head` | publish `bound` |

`head == 0` means unverifiable, so it goes to the search. If real geth nodes turn out to omit
`head`, invert that rule to "trust when absent". If live op-sepolia ever reports `oldestBlock` a
few blocks below `head.number` instead of equal, add a tolerance constant in `proof_bound.go` and
compare `bound + tolerance >= head`; keep it under 128 so a geth full node reporting `head-128`
stays trusted.

## Wiring

`internal/upstreams/chains_specific/evm_specific/evm_chain_specific.go`

- Bound detector: attached when the chain spec has `eth_getProof`. No `debug_proofsSyncStatus`
  gate: the detector asks the method every cycle and treats a rejection as "next stage".
- Label detector `EthHistoricalProofsLabelsDetector`: gated on `eth_getProof` as well. The label
  states what backs that method, and `debug_proofsSyncStatus` sits in the base `eth-json-rpc` spec
  every EVM chain inherits, so gating on it excluded nothing while chains that disable
  `eth_getProof` (viction, hyperliquid) still ran the detector.

## Label

`internal/upstreams/labels/eth_labels/eth_historical_proofs_detector.go`, unchanged by the
refactor. It makes its own `debug_proofsSyncStatus` call, because the label and lower-bound
processors run on different schedules. `historical_proofs=true` when the method answers with a
window, even an empty one, because the store exists. `false` when the method is definitively
absent. Nothing on transient or malformed answers, so the last verdict stands.

## Dependencies

`github.com/drpcorg/public` v1.3.1 ships the spec entry
`{"name": "debug_proofsSyncStatus", "group": "debug", "params": [], "settings": {"cacheable": false}}`.
`cacheable: false` matters: `internal/caches/cache_policies.go` obeys `method.IsCacheable()`, which
defaults to true, so without the setting the window would be served from cache. The method also
joined `probedMethods` in `evm_methods/method_probe_detector.go`, so a node that lists the `debug`
module but is not op-reth stops advertising it.

No protocol, flow, chain-aggregation or emerald mapping changes: `ProofBound` behaves like every
other lower bound. No upper bound type exists; `drpcorg/public` v1.3.1 reserves enum 11 unused.

## Verification

Unit: `internal/upstreams/lower_bounds/evm_bounds/proof_bound_test.go` covers a sync-status window
with one upstream call, decimal input with `earliest = 0`, three rejection shapes asked twice
across two cycles, six malformed or empty bodies, `oldestBlock == head.number`, a report without a
head, `disabled: true`, and a detector without capabilities.
`evm_chain_specific_test.go` pins both `eth_getProof` gates.

Runtime, against a fake op-reth on optimism (head 5000000, window 129600):

- Sync-status mode: `lower bound of type PROOF is 4870400`, `eth_getProof` count 0.
- op-sepolia shape: the debug line `reports proofs from 5000000 with head 5000000, ignoring it for
  the proof bound`, then `PROOF is 4000000`, the height the search finds, never the head.
- Two cycles against a node rejecting the method: `debug_proofsSyncStatus` requested twice.
