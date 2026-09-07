# op-reth `debug_proofsSyncStatus` support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** nodecore reads the historical proof window of op-reth upstreams via `debug_proofsSyncStatus`, publishes `earliest` as `LOWER_BOUND_PROOF`, `latest` as `LOWER_BOUND_PROOF_UPPER` and label `historical_proofs=true`, and honours inbound `LOWER_BOUND_PROOF_UPPER` selectors.

**Architecture:** The existing proof lower-bound detector (`EvmLowerBoundDetector` with `MainBoundType == ProofBound`) gets a first-priority source, `EvmProofsSyncStatus`, ahead of `eth_capabilities` and the `eth_getProof` binary search. A new internal type `protocol.UpperProofBound` is the first "upper edge" bound: processor, prediction, state events, chain aggregation and the selector matcher treat it with inverted rules (decreases accepted, value 1 is not archive, chain-level max, match when `predicted >= height`). A label detector in `eth_labels` publishes `historical_proofs`.

**Tech Stack:** Go, testify mocks (`pkg/test_utils/mocks`), `github.com/drpcorg/public` (method specs + `dshackle` protobuf types).

## Global Constraints

- Cross-repo contract (released in `drpcorg/public` v1.2.2, https://github.com/drpcorg/public/releases/tag/v1.2.2): `proto/blockchain.proto` has `LOWER_BOUND_PROOF_UPPER = 11` (generated `dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER`, `pkg/dshackle/blockchain.pb.go:39`), and `pkg/methods/specs/eth-json-rpc.json:162-167` has `{"name": "debug_proofsSyncStatus", "group": "debug", "params": [], "settings": {"cacheable": false}}`. The `LowerBound` message is reused for the upper bound. Label name `historical_proofs`, value `true`. nodecore bumps `github.com/drpcorg/public` from v1.1.0 to v1.2.2 in Task 0.
- Inbound `LowerHeightSelector{height, lower_bound_type=LOWER_BOUND_PROOF_UPPER}` means: match only nodes whose predicted upper proof bound is `>= height`; nodes with no upper bound match (proto comment on `LowerHeightSelector` in v1.2.2 states the same inversion).
- Save this plan to `docs/superpowers/plans/2026-09-02-proofs-sync-status.md` (sibling of the existing `docs/superpowers/specs/` design docs) as the first commit.
- Do not run project-wide formatters, linters, or the full test suite while working. Run only the packages you touch. Run `go build ./...` once at the end.
- Commit messages: no generated-by or co-author lines.

---

## Reference re-verification (2026-09-02 tree)

Confirmed as briefed: `internal/upstreams/lower_bounds/detector.go:10-14`; `evm_bounds/proof_bound.go:14-29`; `evm_bounds/capabilities.go:265-323`; `evm_chain_specific.go:55-86,94-102,139-163`; `lower_bound_processor.go:131-166`; `chain_event_mapper.go:64-156` (ProofBound at 147-148); `flow/selectors.go:124-128,198-211`; `protocol/data.go:271-278`; `emerald-grpc/proto/blockchain.proto:137-149`; `eth_client_detector.go:102-133`.

Drift:
- `internal/protocol/lower_bounds.go:10-22`: `ProofBound` is **9**, not 8 (`UnknownBound = iota + 1`, so Slot=2 … Trace=8, Proof=9, Epoch=10, Blob=11).
- `evm_bounds/evm_lower_bound.go`: `DetectLowerBound` is at 75-83, `probe` at 85-100 (briefed as 78-100).
- `selector_mapper.go`: `mapDshackleLowerHeightSelector` 102-122, `mapDshackleLowerBoundType` 124-149 (briefed as 111-147).
- `methods.go`: `NewUpstreamMethods` 24-73; 140-144 is inside `IsForceEnabled` (135-153).
- `emerald-grpc/` is a separate Go module `github.com/drpcorg/emerald-grpc` (`emerald-grpc/go.mod`) that only embeds the protos (`embed.go`). Nothing in nodecore imports it. Go types come from `github.com/drpcorg/public/pkg/dshackle` (`go.mod:11`, currently v1.1.0).

Not in the brief, load-bearing:
- `internal/protocol/upstream_state_events.go:34-46` `LowerBoundUpstreamStateEvent.Same` drops any bound that moves backwards (unless 1) or exceeds the head. Must be relaxed for the upper bound.
- `internal/upstreams/chain_supervisor_state.go:163-180` `processLowerBounds` keeps the **minimum** per type across upstreams. The upper bound needs the maximum.
- `internal/upstreams/upstream.go:178-190` `GenericUpstream.PredictLowerBound` = max(processor prediction, last stored bound). Head is available here (`state.HeadData.Height`) — the place to cap the upper prediction.
- `internal/upstreams/flow/request_processor.go:228,284-301` live corrections raise `ProofBound` to `block+1` on pruned errors for `eth_getProof`.
- `internal/caches/cache_policies.go:256-263`: cacheability comes from the spec (`method.IsCacheable()`); `public/pkg/methods/method.go:167` defaults `cacheable = true`. Without `"cacheable": false` in the spec, `debug_proofsSyncStatus` responses would be cached.
- `public/pkg/methods/method_groups.go:41-50`: every grouped method is also indexed into `default`, so a `debug`-group method is user-callable by default.
- `evm_methods/method_probe_detector.go:22-31`: `probedMethods` is a static list; `debug_*` methods are otherwise trusted whenever the node lists the `debug` module (`rpc_modules_detector.go:17-21`).
- Local `../public` checkout is behind: it has neither `LOWER_BOUND_PROOF_UPPER` nor `debug_proofsSyncStatus`. The v1.2.2 tag has both (verified from `raw.githubusercontent.com/drpcorg/public/v1.2.2/proto/blockchain.proto` and `.../pkg/methods/specs/eth-json-rpc.json`).
- `../dproxy/pkg/lower_data/lower_data.go:169-196` confirmed: unknown enum → `Unknown` (default branch 193-194); same in `pkg/reducer_states/provider_state.go:810-811`.

---

## Task 0: Bump `github.com/drpcorg/public` to v1.2.2

**Files:** `go.mod`, `go.sum`

- [ ] **Step 1:** `go get github.com/drpcorg/public@v1.2.2 && go mod tidy`.
- [ ] **Step 2:** Confirm `grep -n LOWER_BOUND_PROOF_UPPER $(go env GOMODCACHE)/github.com/drpcorg/public@v1.2.2/pkg/dshackle/blockchain.pb.go` prints `LowerBoundType_LOWER_BOUND_PROOF_UPPER LowerBoundType = 11`.
- [ ] **Step 3:** `go build ./... && go test ./internal/upstreams/methods/... ./internal/upstreams/chains_specific/evm_specific/` → PASS. If a spec-pinning test fails because `debug_proofsSyncStatus` is now in the `debug`/`default` groups, extend that test's expected set with the method (the spec change is intended).
- [ ] **Step 4: Commit** `chore(deps): bump drpcorg/public to v1.2.2`

After this task `hasMethod("debug_proofsSyncStatus")` is true for every `eth`-spec chain (optimism/base/ink import `eth` → `eth-json-rpc`), so the wiring in Tasks 4-6 is live as soon as it lands.


## Task 1: `protocol.UpperProofBound`

**Files:**
- Modify: `internal/protocol/lower_bounds.go:10-50`

**Interfaces produced:** `protocol.UpperProofBound` (value 12, `String() == "UPPER_PROOF"`), `func (t LowerBoundType) IsUpperBound() bool`.

- [ ] **Step 1: Add the enum member and helper**

```go
const (
	UnknownBound LowerBoundType = iota + 1
	SlotBound
	StateBound
	ReceiptsBound
	TxBound
	BlockBound
	LogsBound
	TraceBound
	ProofBound
	EpochBound
	BlobBound
	// UpperProofBound is the newest block whose eth_getProof is served from a historical
	// proof store (op-reth debug_proofsSyncStatus "latest"). It is the only upper edge
	// among the bounds: it may move backwards, value 1 is not an archive marker, and
	// routing admits an upstream when the predicted value is >= the requested height.
	UpperProofBound
)

// IsUpperBound reports whether the type is the upper edge of a data window rather than
// a lower one. Processor, prediction, state and routing rules invert for it.
func (t LowerBoundType) IsUpperBound() bool {
	return t == UpperProofBound
}
```

In `String()` add `case UpperProofBound: return "UPPER_PROOF"` before the `panic`.

- [ ] **Step 2: Commit** `feat(protocol): add UpperProofBound lower bound type`

---

## Task 2: Processor, prediction, state event, chain aggregation, head cap

**Files:**
- Modify: `internal/upstreams/lower_bounds/lower_bound_processor.go:148-160`
- Modify: `internal/upstreams/lower_bounds/lower_bounds.go:31-71`
- Modify: `internal/protocol/upstream_state_events.go:34-46`
- Modify: `internal/upstreams/chain_supervisor_state.go:163-180`
- Modify: `internal/upstreams/upstream.go:178-190`
- Test: `internal/upstreams/lower_bounds/lower_bound_processor_test.go`, `internal/upstreams/lower_bounds/lower_bounds_test.go`, `internal/protocol/upstream_state_events_test.go`, `internal/upstreams/chain_supervisor_test.go`, `internal/upstreams/upstream_test.go`

**Interfaces consumed:** `protocol.UpperProofBound`, `LowerBoundType.IsUpperBound()`.

- [ ] **Step 1: Write failing processor tests** (`lower_bound_processor_test.go`, copy the shape of `TestGenericLowerBoundServiceIgnoresLowerBoundThatMovesBackwards` at 197-221)

```go
func TestGenericLowerBoundServicePublishesUpperBoundDecrease(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(200, 1000, protocol.UpperProofBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(150, 1001, protocol.UpperProofBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()
	done := startService(t, service)
	defer stopService(t, service, done)

	first := waitForLowerBound(t, sub.Events, 200*time.Millisecond)
	second := waitForLowerBound(t, sub.Events, 200*time.Millisecond)

	assert.Equal(t, int64(200), first.Bound)
	assert.Equal(t, int64(150), second.Bound, "an upper edge moving backwards (proofs unwind) is published")
	assert.Equal(t, int64(150), service.PredictLowerBound(protocol.UpperProofBound, 0))
	detector.AssertExpectations(t)
}

// Value 1 is a real block for an upper edge, not the archive marker: the prediction keeps
// moving at chain speed instead of being pinned to 1.
func TestGenericLowerBoundServiceUpperBoundOneIsNotArchive(t *testing.T) {
	detector := mocks.NewLowerBoundDetectorMock()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(1, protocol.UpperProofBound),
	}, nil).Once()
	detector.On("DetectLowerBound", mock.Anything).Return([]protocol.LowerBoundData(nil), nil).Maybe()
	detector.On("Period").Return(20 * time.Millisecond).Maybe()

	service := lower_bounds.NewGenericLowerBoundProcessorWithDelay(context.Background(), "up-1", 0.5, time.Millisecond, []lower_bounds.LowerBoundDetector{detector})
	sub := service.Subscribe("sub-1")
	defer sub.Unsubscribe()
	done := startService(t, service)
	defer stopService(t, service, done)

	waitForLowerBound(t, sub.Events, 200*time.Millisecond)
	predicted := service.PredictLowerBound(protocol.UpperProofBound, 100)
	assert.GreaterOrEqual(t, predicted, int64(50))
	assert.LessOrEqual(t, predicted, int64(52))
	detector.AssertExpectations(t)
}
```

`lower_bounds_test.go`: an upper edge never trains the regression; three rising points keep `k = averageSpeed` and the line passes through the newest point.

```go
func TestUpperBoundPredictsAtChainSpeedFromLastValue(t *testing.T) {
	lb := lower_bounds.NewLowerBounds(0.5)
	base := mustParseUTC(t, "28.08.2025 11:00:00").Unix()
	// the store catches up faster than the chain (resync): 1000 blocks per minute
	lb.UpdateBound(protocol.NewLowerBoundData(10_000, base, protocol.UpperProofBound))
	lb.UpdateBound(protocol.NewLowerBoundData(11_000, base+60, protocol.UpperProofBound))
	lb.UpdateBound(protocol.NewLowerBoundData(12_000, base+120, protocol.UpperProofBound))

	// 100s later: 12000 + 0.5*100, not the 1666 the regression would extrapolate
	assert.Equal(t, int64(12_050), lb.PredictNextBoundAtSpecificTime(protocol.UpperProofBound, base+220))

	// unwind: the line restarts from the new, lower value
	lb.UpdateBound(protocol.NewLowerBoundData(9_000, base+180, protocol.UpperProofBound))
	assert.Equal(t, int64(9_020), lb.PredictNextBoundAtSpecificTime(protocol.UpperProofBound, base+220))
}
```

- [ ] **Step 2: Run** `go test ./internal/upstreams/lower_bounds/ -run 'UpperBound' -v` → FAIL (second publish never arrives / prediction pinned).

- [ ] **Step 3: Implement**

`lower_bound_processor.go:157`:
```go
		// an upper edge is taken as reported: decreases (proofs unwind, resync) are real
		if data.Type.IsUpperBound() || data.Bound >= bound || data.Bound == 1 {
			b.publishBound(data, boundsChan)
		}
```

`lower_bounds.go` `UpdateBound` — insert a first case:
```go
	switch {
	case newBound.Type.IsUpperBound():
		// An upper edge follows the chain tip: a straight line at chain speed through the
		// newest observation. No regression - a store catching up after init moves faster
		// than the chain and would over-predict; decreases are taken as-is.
		lb.resetBound(coeffs, newBound, lb.averageSpeed, lb.calculateB(newBound))

	case newBound.Bound == 1:
```
`lower_bounds.go` `initBound:64`: `if newBound.Bound == 1 && !newBound.Type.IsUpperBound() {`.

`upstream_state_events.go` `Same` — insert at the top:
```go
	if l.Data.Type.IsUpperBound() {
		// an upper edge is applied as reported: it may move backwards (proofs unwind,
		// resync) and may run ahead of the head nodecore polled last
		current, ok := state.LowerBoundsInfo.GetLowerBound(l.Data.Type)
		return ok && current == l.Data
	}
```
Update the doc comment at 28-33 to mention the upper-edge exception.

`chain_supervisor_state.go:172-175`:
```go
			currentBound, ok := bounds[bound.Type]
			if !ok || widensChainWindow(bound, currentBound) {
				bounds[bound.Type] = bound
			}
```
add below `processLowerBounds`:
```go
// widensChainWindow reports whether candidate extends the chain-wide data window over
// current: the lowest lower edge, the highest upper edge.
func widensChainWindow(candidate, current protocol.LowerBoundData) bool {
	if candidate.Type.IsUpperBound() {
		return candidate.Bound > current.Bound
	}
	return candidate.Bound < current.Bound
}
```

`upstream.go` `PredictLowerBound` — append before `return predicted`:
```go
	// an upper edge cannot be ahead of the node itself
	if boundType.IsUpperBound() && predicted > 0 {
		if head := int64(state.HeadData.Height); head > 0 && predicted > head {
			predicted = head
		}
	}
```
`timeOffset` is passed through unchanged for the upper bound: the caller's offset semantics are the same, and the head cap bounds the optimism.

- [ ] **Step 4: Add the remaining tests**

`upstream_state_events_test.go` (next to `TestLowerBoundUpstreamStateEventNeverLowersExceptArchiveReset`, reuse its `apply`/`boundOf` closures by extracting them to package-level helpers if you prefer):
```go
func TestLowerBoundUpstreamStateEventAppliesUpperEdgeAsReported(t *testing.T) {
	// same apply/boundOf as above
	state := newUpstreamState()
	state.HeadData = protocol.NewBlockWithHeight(1000)
	state = apply(state, protocol.NewLowerBoundData(900, 100, protocol.UpperProofBound))
	assert.Equal(t, int64(900), boundOf(state, protocol.UpperProofBound))
	state = apply(state, protocol.NewLowerBoundData(800, 101, protocol.UpperProofBound))
	assert.Equal(t, int64(800), boundOf(state, protocol.UpperProofBound), "upper edge may move backwards")
	state = apply(state, protocol.NewLowerBoundData(1002, 102, protocol.UpperProofBound))
	assert.Equal(t, int64(1002), boundOf(state, protocol.UpperProofBound), "upper edge may run ahead of the polled head")
	event := &protocol.LowerBoundUpstreamStateEvent{Data: protocol.NewLowerBoundData(1002, 102, protocol.UpperProofBound)}
	assert.True(t, event.Same(state), "identical republish is a no-op")
}
```

`chain_supervisor_test.go`: build two upstream events with the existing helper at `chain_supervisor_test.go:91` (`state.LowerBoundsInfo = lowerBoundsInfo`), one with `ProofBound=100, UpperProofBound=5000`, one with `ProofBound=4900` only. Assert the chain state has `ProofBound.Bound == 100` and `UpperProofBound.Bound == 5000`; then add a third upstream with `UpperProofBound=6000` and assert `6000`.

`upstream_test.go`: with the existing upstream construction (`newUpstreamConfig` at 753 and the emit helpers around 150-190), set head to 1000 via `UpdateHead(1000, 0)`, emit `UpperProofBound=990` through the lower-bound processor mock (`mocks.NewLowerBoundProcessorMock()`, `On("PredictLowerBound", protocol.UpperProofBound, int64(0)).Return(int64(1010))`) and assert `upstream.PredictLowerBound(protocol.UpperProofBound, 0) == 1000`. Also assert a lower type is not capped: `On("PredictLowerBound", protocol.StateBound, int64(0)).Return(int64(1010))` → `1010`.

- [ ] **Step 5: Run** `go test ./internal/upstreams/lower_bounds/ ./internal/protocol/ ./internal/upstreams/ -run 'UpperBound|UpperEdge|ChainSupervisorLowerBounds|PredictLowerBound'` → PASS. Also run `go test ./internal/upstreams/lower_bounds/ ./internal/protocol/` fully (fast packages) to confirm the lower-bound rules are unchanged.

- [ ] **Step 6: Commit** `feat(lower-bounds): treat UpperProofBound as an upper edge`

---

## Task 3: `LOWER_BOUND_PROOF_UPPER` selector matcher

**Files:**
- Modify: `internal/upstreams/flow/matchers.go` (after `LowerHeightMatcher`, ~360)
- Modify: `internal/upstreams/flow/selectors.go:124-128`
- Test: `internal/upstreams/flow/matchers_test.go`, `internal/upstreams/flow/selectors_test.go`

**Interfaces produced:** `NewUpperHeightMatcher(height int64, boundType protocol.LowerBoundType, timeOffset int64, predict LowerHeightPredictor) *UpperHeightMatcher`, `UpperHeightResponse`.

- [ ] **Step 1: Failing tests**

`matchers_test.go` (extend `TestSelectorLabelExistsHeightSlotAndLowerMatchers` or add):
```go
func TestUpperHeightMatcher(t *testing.T) {
	methodsMock := mocks.NewMethodsMock()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "", nil, nil)
	predict := func(v int64) LowerHeightPredictor {
		return func(upstreamId string, boundType protocol.LowerBoundType, timeOffset int64) int64 {
			assert.Equal(t, protocol.UpperProofBound, boundType)
			return v
		}
	}
	assert.Equal(t, SuccessType, NewUpperHeightMatcher(500, protocol.UpperProofBound, 0, predict(600)).Match("up", &state).Type())
	assert.Equal(t, SuccessType, NewUpperHeightMatcher(500, protocol.UpperProofBound, 0, predict(500)).Match("up", &state).Type())
	assert.Equal(t, SuccessType, NewUpperHeightMatcher(500, protocol.UpperProofBound, 0, predict(0)).Match("up", &state).Type(), "no upper bound published: not a windowed node, admitted")
	resp := NewUpperHeightMatcher(500, protocol.UpperProofBound, 0, predict(400)).Match("up", &state)
	assert.Equal(t, SelectorType, resp.Type())
	assert.Equal(t, "Upstream upper height 400 of type UPPER_PROOF is less than 500", resp.Cause())
}
```

`selectors_test.go`:
```go
func TestCompileUpperBoundSelector(t *testing.T) {
	matcher, sort := compileSelector(protocol.RequestLowerHeightSelector{Height: 500, LowerBoundType: protocol.UpperProofBound}, nil)
	assert.IsType(t, &UpperHeightMatcher{}, matcher)
	assert.Nil(t, sort)

	matcher, sort = compileSelector(protocol.RequestLowerHeightSelector{Height: 0, LowerBoundType: protocol.UpperProofBound}, nil)
	assert.IsType(t, &UnsupportedSelectorMatcher{}, matcher)
	assert.Nil(t, sort)
}
```

- [ ] **Step 2: Run** `go test ./internal/upstreams/flow/ -run 'UpperHeight|UpperBoundSelector'` → FAIL (undefined symbols).

- [ ] **Step 3: Implement**

`matchers.go` after `LowerHeightMatcher.Match` (line 360):
```go
// UpperHeightMatcher admits upstreams whose predicted upper edge of boundType reaches
// height. An upstream without that bound is admitted: it is not a windowed node, and the
// lower-bound selectors accompanying the request decide for it.
type UpperHeightMatcher struct {
	height     int64
	boundType  protocol.LowerBoundType
	timeOffset int64
	predict    LowerHeightPredictor
}

func NewUpperHeightMatcher(height int64, boundType protocol.LowerBoundType, timeOffset int64, predict LowerHeightPredictor) *UpperHeightMatcher {
	return &UpperHeightMatcher{height: height, boundType: boundType, timeOffset: timeOffset, predict: predict}
}

func (u *UpperHeightMatcher) Match(upId string, _ *protocol.UpstreamState) MatchResponse {
	predicted := int64(0)
	if u.predict != nil {
		predicted = u.predict(upId, u.boundType, u.timeOffset)
	}
	if predicted == 0 || predicted >= u.height {
		return SuccessResponse{}
	}
	return UpperHeightResponse{u.height, predicted, u.boundType}
}
```
and next to `LowerHeightResponse` (212-223):
```go
type UpperHeightResponse struct {
	height, predictedHeight int64
	boundType               protocol.LowerBoundType
}

func (u UpperHeightResponse) Type() MatchResponseType { return SelectorType }
func (u UpperHeightResponse) Cause() string {
	return fmt.Sprintf("Upstream upper height %d of type %s is less than %d", u.predictedHeight, u.boundType.String(), u.height)
}
```

`selectors.go:124-128`:
```go
	case protocol.RequestLowerHeightSelector:
		if s.LowerBoundType.IsUpperBound() {
			// HeightDelta is a lower-edge tolerance; the upper-edge contract defines height only.
			if s.Height == 0 {
				return unsupported(fmt.Sprintf("lower height selector of type %s requires a height", s.LowerBoundType.String()))
			}
			return NewUpperHeightMatcher(s.Height, s.LowerBoundType, s.TimeOffset, predict), nil
		}
		if s.Height == 0 {
			return sortOnly(sortSpec{kind: sortPredictedLowerBoundAsc, lowerBoundType: s.LowerBoundType, timeOffset: s.TimeOffset})
		}
		return NewLowerHeightMatcher(s.Height, s.LowerBoundType, s.TimeOffset, s.HeightDelta, predict), nil
```
`Height == 0` with an upper type fails closed (like `LOWER_BOUND_UNSPECIFIED` in `mapDshackleLowerHeightSelector`); no sort semantics are defined for it.

- [ ] **Step 4: Run** `go test ./internal/upstreams/flow/` → PASS.
- [ ] **Step 5: Commit** `feat(flow): match LOWER_BOUND_PROOF_UPPER selectors against the predicted upper edge`

---

## Task 4: `EvmProofsSyncStatus` inside the proof detector

**Files:**
- Create: `internal/upstreams/lower_bounds/evm_bounds/proofs_sync_status.go`
- Modify: `internal/upstreams/lower_bounds/evm_bounds/evm_lower_bound.go:29-38,40-45,75-83`
- Modify: `internal/upstreams/lower_bounds/evm_bounds/evm_util.go` (add `parseEvmBlockNumber`)
- Modify: `internal/upstreams/chains_specific/evm_specific/evm_chain_specific.go:148-150`
- Test: `internal/upstreams/lower_bounds/evm_bounds/proofs_sync_status_test.go` (new)

**Decision — one detector, three sources in priority order.** `debug_proofsSyncStatus` is tried first inside the existing proof detector, then `eth_capabilities`, then the `eth_getProof` binary search. A second detector publishing `ProofBound` beside the search detector would race on the same type and still pay ~30 `eth_getProof` probes per cycle on a node that can answer in one call. Priority-within-one-detector keeps a single `ProofBound` source, keeps `SupportedTypes()` honest for the capabilities/search fan-out, and mirrors how `EvmCapabilities` was slotted in. Because there is no separate `LowerBoundDetector` type, the helper is named `EvmProofsSyncStatus` (not `EvmProofsSyncStatusDetector`), matching `EvmCapabilities`.

`UpperProofBound` is **not** added to `SupportedTypes()`: `detectFromCapabilities` (capabilities.go:311-316) requires every supported type to be covered and would stop taking over for proofs, and `LowerBoundResults` (lower_bound_search.go:135-148) would fan the search result out as the upper bound. `SupportedTypes()` has no other consumer than the error log at lower_bound_processor.go:143.

- [ ] **Step 1: Failing tests** (`proofs_sync_status_test.go`, package `evm_bounds_test`, reuse `evmChain`, `evmOK`, `matchEvmRequest`, `expectCapabilities`, `countRequests`, `expectLatest`, `expectBlocksAbove` from the sibling test files)

```go
func expectProofsSyncStatus(connector *mocks.ConnectorMock, response protocol.ResponseHolder) *mock.Call {
	return connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchEvmRequest("debug_proofsSyncStatus"))).
		Return(response)
}

func proofDetectorWithSyncStatus(connector *mocks.ConnectorMock) (*evm_bounds.EvmLowerBoundDetector, *evm_bounds.EvmProofsSyncStatus) {
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	syncStatus := evm_bounds.NewEvmProofsSyncStatus("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).
		WithCapabilities(capabilities).
		WithProofsSyncStatus(syncStatus)
	return detector, syncStatus
}

func boundsByType(bounds []protocol.LowerBoundData) map[protocol.LowerBoundType]int64 {
	result := make(map[protocol.LowerBoundType]int64, len(bounds))
	for _, b := range bounds {
		result[b.Type] = b.Bound
	}
	return result
}

func TestProofsSyncStatusYieldsLowerAndUpperProofBounds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	detector, _ := proofDetectorWithSyncStatus(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(result))
	assert.Len(t, connector.Calls, 1, "no eth_capabilities, no eth_getProof search")
	assert.Equal(t, []protocol.LowerBoundType{protocol.ProofBound}, detector.SupportedTypes())
}

func TestProofsSyncStatusAcceptsDecimalNumbersAndCoercesEarliestZero(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":0,"latest":200}`)).Once()
	detector, _ := proofDetectorWithSyncStatus(connector)

	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 1, protocol.UpperProofBound: 200}, boundsByType(result))
}

func TestProofsSyncStatusUnsupportedFallsBackWithoutReprobe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respErr *protocol.ResponseError
	}{
		{"json-rpc code -32601", protocol.NotSupportedMethodError("debug_proofsSyncStatus")},
		{"textual method not found", protocol.ResponseErrorWithMessage("Method not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(tc.respErr)).Once()
			// capabilities answer the proof bound so the search never runs
			expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
			detector, _ := proofDetectorWithSyncStatus(connector)

			first, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

			second, err := detector.DetectLowerBound(context.Background())
			require.NoError(t, err)
			assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(second))
			assert.Equal(t, 1, countRequests(connector, "debug_proofsSyncStatus"))
		})
	}
}

func TestProofsSyncStatusMalformedResponseIsUnsupported(t *testing.T) {
	for _, body := range []string{`"garbage"`, `null`, `{"earliest":"0x64"}`, `{"earliest":"latest","latest":"0xc8"}`} {
		t.Run(body, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			expectProofsSyncStatus(connector, evmOK(body)).Once()
			expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
			detector, _ := proofDetectorWithSyncStatus(connector)

			for i := 0; i < 2; i++ {
				result, err := detector.DetectLowerBound(context.Background())
				require.NoError(t, err)
				assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(result))
			}
			assert.Equal(t, 1, countRequests(connector, "debug_proofsSyncStatus"))
		})
	}
}

// A window that is not (yet) usable is not a verdict: an initialising store answers
// latest 0 or earliest > latest and must be asked again next cycle.
func TestProofsSyncStatusEmptyWindowFallsBackAndRetries(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x0","latest":"0x0"}`)).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, _ := proofDetectorWithSyncStatus(connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
}

func TestProofsSyncStatusTransientErrorFallsBackAndRetries(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom"))).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, _ := proofDetectorWithSyncStatus(connector)

	first, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(first))

	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
	assert.Equal(t, 2, countRequests(connector, "debug_proofsSyncStatus"))
}

func TestProofsSyncStatusReprobesAfterInterval(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectProofsSyncStatus(connector, protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus"))).Once()
	expectProofsSyncStatus(connector, evmOK(`{"earliest":"0x64","latest":"0xc8"}`)).Once()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Maybe()
	detector, syncStatus := proofDetectorWithSyncStatus(connector)
	syncStatus.SetReprobeInterval(0)

	_, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	second, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 100, protocol.UpperProofBound: 200}, boundsByType(second))
}

func TestProofDetectorWithoutSyncStatusNeverAsks(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectCapabilities(connector, evmOK(`{"stateproofs":{"disabled":false,"oldestBlock":"0x2a"}}`)).Once()
	capabilities := evm_bounds.NewEvmCapabilities("id", evmChain(), time.Second, connector)
	detector := evm_bounds.NewEvmProofLowerBoundDetector("id", evmChain(), time.Second, connector).WithCapabilities(capabilities)

	result, err := detector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[protocol.LowerBoundType]int64{protocol.ProofBound: 42}, boundsByType(result))
	assert.Equal(t, 0, countRequests(connector, "debug_proofsSyncStatus"))
}
```

- [ ] **Step 2: Run** `go test ./internal/upstreams/lower_bounds/evm_bounds/ -run ProofsSyncStatus` → FAIL (undefined `NewEvmProofsSyncStatus`, `WithProofsSyncStatus`).

- [ ] **Step 3: Implement `proofs_sync_status.go`**

```go
package evm_bounds

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	evmProofsSyncStatusMethod = "debug_proofsSyncStatus"

	// An upstream without the method is re-probed occasionally: nodes get upgraded.
	evmProofsSyncStatusReprobeInterval = time.Hour
)

// EvmProofsSyncStatus asks an op-reth upstream for the block window its historical proof
// store serves (debug_proofsSyncStatus -> {"earliest","latest"}). It is the first source
// the proof detector consults: one call replaces the eth_getProof binary search for the
// lower proof bound and is the only source of the upper proof bound. An upstream that
// rejects the method as unknown, or answers with an unparseable body, is remembered as
// unsupported and re-asked hourly; transient failures leave the verdict alone.
type EvmProofsSyncStatus struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	connector       connectors.ApiConnector
	reprobeInterval time.Duration

	mu            sync.Mutex
	unsupported   bool
	lastAttemptAt time.Time
}

func NewEvmProofsSyncStatus(
	upstreamId string,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmProofsSyncStatus {
	return &EvmProofsSyncStatus{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
		reprobeInterval: evmProofsSyncStatusReprobeInterval,
	}
}

// SetReprobeInterval overrides how often an unsupported upstream is re-asked. Production
// relies on the default; tests shrink it.
func (s *EvmProofsSyncStatus) SetReprobeInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reprobeInterval = interval
}

// evmProofWindow is the inclusive block range served from the proof store.
type evmProofWindow struct {
	earliest int64
	latest   int64
}

// window returns the upstream's current proof window, or nil when there is no usable one:
// method unsupported (cached verdict), transient failure, malformed body, or a store that
// is still empty (latest 0 or earliest > latest).
func (s *EvmProofsSyncStatus) window(ctx context.Context) *evmProofWindow {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.unsupported && time.Since(s.lastAttemptAt) < s.reprobeInterval {
		return nil
	}
	s.lastAttemptAt = time.Now()

	response, err := s.send(ctx)
	if err != nil {
		log.Debug().Err(err).Msgf("couldn't request %s from upstream '%s'", evmProofsSyncStatusMethod, s.upstreamId)
		return nil
	}
	if response.HasError() {
		respErr := response.GetError()
		if isEvmMethodNotFoundError(respErr) {
			s.markUnsupported(respErr.Message)
			return nil
		}
		log.Debug().Err(respErr).Msgf("couldn't fetch %s from upstream '%s'", evmProofsSyncStatusMethod, s.upstreamId)
		return nil
	}

	window, err := parseEvmProofsSyncStatus(response.ResponseResult())
	if err != nil {
		s.markUnsupported(err.Error())
		return nil
	}
	if s.unsupported {
		log.Info().Msgf("upstream '%s' supports %s, using it for proof bound detection", s.upstreamId, evmProofsSyncStatusMethod)
	}
	s.unsupported = false
	if window.latest == 0 || window.earliest > window.latest {
		log.Debug().Msgf("upstream '%s' reports an empty proof window [%d, %d]", s.upstreamId, window.earliest, window.latest)
		return nil
	}
	return window
}

func (s *EvmProofsSyncStatus) send(ctx context.Context) (protocol.ResponseHolder, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(evmProofsSyncStatusMethod, []any{}, s.chain.Chain)
	if err != nil {
		return nil, err
	}
	return s.connector.SendRequest(ctx, request), nil
}

func (s *EvmProofsSyncStatus) markUnsupported(cause string) {
	if !s.unsupported {
		log.Debug().Msgf("upstream '%s' doesn't support %s (%s), falling back to eth_capabilities/search", s.upstreamId, evmProofsSyncStatusMethod, cause)
	}
	s.unsupported = true
}

type evmProofsSyncStatusResponse struct {
	Earliest json.RawMessage `json:"earliest"`
	Latest   json.RawMessage `json:"latest"`
}

// parseEvmProofsSyncStatus maps the raw result to a window. A missing or unparseable
// field is malformed. earliest 0x0 is coerced to 1: nodecore's convention for "from the
// first block" is bound 1, and a 0 prediction reads as "unknown" to routing.
func parseEvmProofsSyncStatus(raw []byte) (*evmProofWindow, error) {
	if isEvmNullResult(raw) {
		return nil, fmt.Errorf("null result")
	}
	parsed := evmProofsSyncStatusResponse{}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	if len(parsed.Earliest) == 0 || len(parsed.Latest) == 0 {
		return nil, fmt.Errorf("malformed response: earliest or latest missing")
	}
	earliest, err := parseEvmBlockNumber(parsed.Earliest)
	if err != nil || earliest < 0 {
		return nil, fmt.Errorf("malformed earliest: %w", err)
	}
	latest, err := parseEvmBlockNumber(parsed.Latest)
	if err != nil || latest < 0 {
		return nil, fmt.Errorf("malformed latest: %w", err)
	}
	if earliest == 0 {
		earliest = 1
	}
	return &evmProofWindow{earliest: earliest, latest: latest}, nil
}
```

`evm_util.go` — add below `parseHexInt`:
```go
// parseEvmBlockNumber accepts both JSON shapes a block number is reported in: a quoted hex
// string ("0x1a") and a bare decimal number (26). parseHexInt alone would read a bare 26
// as 0x26.
func parseEvmBlockNumber(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		return parseHexInt(raw)
	}
	return strconv.ParseInt(trimmed, 10, 64)
}
```
The wire shape of `earliest`/`latest` is `unverified — confirm first` against a live op-reth (`curl -d '{"jsonrpc":"2.0","id":1,"method":"debug_proofsSyncStatus","params":[]}'`); the parser accepts both, so no code change is expected either way.

`evm_lower_bound.go`:
- struct (29-38): add field `proofsSyncStatus *EvmProofsSyncStatus` after `capabilities`.
- after `WithCapabilities` (40-45):
```go
// WithProofsSyncStatus attaches the debug_proofsSyncStatus source. Only meaningful for
// the ProofBound detector; other detectors ignore it.
func (e *EvmLowerBoundDetector) WithProofsSyncStatus(syncStatus *EvmProofsSyncStatus) *EvmLowerBoundDetector {
	e.proofsSyncStatus = syncStatus
	return e
}
```
- `DetectLowerBound` (75-83): first branch
```go
	if results, ok := e.detectFromProofsSyncStatus(ctx); ok {
		return results, nil
	}
```
- add (in `proofs_sync_status.go`, bottom):
```go
// detectFromProofsSyncStatus resolves the proof window straight from the upstream's
// historical proof store. It emits ProofBound (earliest) and UpperProofBound (latest).
// UpperProofBound is deliberately absent from SupportedTypes: the capabilities and search
// paths cannot produce it, and SupportedTypes drives their fan-out.
func (e *EvmLowerBoundDetector) detectFromProofsSyncStatus(ctx context.Context) ([]protocol.LowerBoundData, bool) {
	if e.proofsSyncStatus == nil || e.MainBoundType != protocol.ProofBound {
		return nil, false
	}
	window := e.proofsSyncStatus.window(ctx)
	if window == nil {
		return nil, false
	}
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(window.earliest, protocol.ProofBound),
		protocol.NewLowerBoundDataNow(window.latest, protocol.UpperProofBound),
	}, true
}
```
- update the struct doc comment (23-28) to mention the third source.

- [ ] **Step 4: Wire it** — `evm_chain_specific.go:148-150`:
```go
	if e.hasMethod("eth_getProof") {
		proofDetector := evm_bounds.NewEvmProofLowerBoundDetector(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector).WithCapabilities(capabilities)
		if e.hasMethod("debug_proofsSyncStatus") {
			proofDetector = proofDetector.WithProofsSyncStatus(evm_bounds.NewEvmProofsSyncStatus(e.upstreamId, e.chain, e.options.InternalTimeout, e.connector))
		}
		detectors = append(detectors, proofDetector)
	}
```
Gating: `hasMethod` (evm_chain_specific.go:154-163) is spec-level (`specs.GetSpecMethod`); with public v1.2.2 (Task 0) the method is in the `eth-json-rpc` spec, so the branch is live for every eth-spec chain. Per-node absence is handled at runtime by the unsupported verdict above.

- [ ] **Step 5: Run** `go test ./internal/upstreams/lower_bounds/evm_bounds/` → PASS (existing capabilities/search tests unchanged: `TestEvmCapabilitiesServeAllBoundTypesWithSingleCall` still sees exactly one call because no sync status is attached there).
- [ ] **Step 6: Commit** `feat(evm-bounds): read the proof window from debug_proofsSyncStatus`

---

## Task 5: `historical_proofs` label detector

**Files:**
- Create: `internal/upstreams/labels/eth_labels/eth_historical_proofs_detector.go`
- Modify: `internal/upstreams/chains_specific/evm_specific/evm_chain_specific.go:59-86`
- Test: `internal/upstreams/labels/eth_labels/eth_historical_proofs_detector_test.go` (new)

**Decision — where and how often.** Lives in `eth_labels` next to `EthArchiveLabelsDetector` (same shape: own RPC call, `DetectLabels() map[string]string`). It makes its own `debug_proofsSyncStatus` call rather than sharing `EvmProofsSyncStatus`: label and lower-bound processors run on different schedules and packages, and the call is one cheap round trip. Period: the shared labels period `e.options.ValidationInterval*5` (evm_chain_specific.go:56; default `ValidationInterval` is 30s per `internal/config/option_defaults.go:81-86` → 2.5 min). Labels are never removed by `GenericLabelsProcessor` (label_processor.go:106-114 only forwards non-empty values), so the detector emits `false` when the method is definitively absent so that a node that loses its proof store self-corrects; transient errors emit nothing.

- [ ] **Step 1: Failing tests** (`eth_historical_proofs_detector_test.go`, pattern from `eth_archive_detector_test.go:17-63`)

```go
func historicalProofsRequest(t *testing.T) protocol.RequestHolder {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("debug_proofsSyncStatus", []any{}, chains.OPTIMISM)
	require.NoError(t, err)
	return request
}

func TestEthHistoricalProofsLabelsDetector(t *testing.T) {
	tests := []struct {
		name     string
		response protocol.ResponseHolder
		expected map[string]string
	}{
		{"window answered", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x64","latest":"0xc8"}`), protocol.JsonRpc), map[string]string{eth_labels.HistoricalProofsLabel: "true"}},
		{"empty store still has a proof store", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x0","latest":"0x0"}`), protocol.JsonRpc), map[string]string{eth_labels.HistoricalProofsLabel: "true"}},
		{"method absent", protocol.NewHttpUpstreamResponseWithError(protocol.NotSupportedMethodError("debug_proofsSyncStatus")), map[string]string{eth_labels.HistoricalProofsLabel: "false"}},
		{"transient error keeps last verdict", protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithMessage("boom")), nil},
		{"malformed keeps last verdict", protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"earliest":"0x64"}`), protocol.JsonRpc), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(test_utils.UpstreamJsonRpcRequestMatcher(historicalProofsRequest(t)))).
				Return(tt.response).
				Once()
			detector := eth_labels.NewEthHistoricalProofsLabelsDetector("id", chains.OPTIMISM, time.Second, connector)

			assert.Equal(t, tt.expected, detector.DetectLabels())
			connector.AssertExpectations(t)
		})
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/upstreams/labels/eth_labels/ -run HistoricalProofs` → FAIL.

- [ ] **Step 3: Implement**

```go
package eth_labels

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// HistoricalProofsLabel marks an upstream whose eth_getProof is backed by a historical
// proof store (op-reth --proofs-history), detected through debug_proofsSyncStatus.
const HistoricalProofsLabel = "historical_proofs"

type EthHistoricalProofsLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	internalTimeout time.Duration
	connector       connectors.ApiConnector
}

func NewEthHistoricalProofsLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthHistoricalProofsLabelsDetector {
	return &EthHistoricalProofsLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
	}
}

type proofsSyncStatusShape struct {
	Earliest json.RawMessage `json:"earliest"`
	Latest   json.RawMessage `json:"latest"`
}

// DetectLabels reports historical_proofs=true when debug_proofsSyncStatus answers with a
// window (even an empty one - the store exists), historical_proofs=false when the upstream
// definitely lacks the method, and nothing on transient or unparseable answers so the last
// verdict stands.
func (e *EthHistoricalProofsLabelsDetector) DetectLabels() map[string]string {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("debug_proofsSyncStatus", []any{}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect historical proofs of upstream '%s'", e.upstreamId)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		if protocol.ClassifyMethodAvailability(response.GetError()) == protocol.MethodNotAvailable {
			return map[string]string{HistoricalProofsLabel: "false"}
		}
		log.Warn().Err(response.GetError()).Msgf("unable to detect historical proofs of upstream '%s'", e.upstreamId)
		return nil
	}

	status := proofsSyncStatusShape{}
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil || len(status.Earliest) == 0 || len(status.Latest) == 0 {
		log.Warn().Msgf("unable to parse debug_proofsSyncStatus of upstream '%s'", e.upstreamId)
		return nil
	}
	return map[string]string{HistoricalProofsLabel: "true"}
}

var _ labels.LabelsDetector = (*EthHistoricalProofsLabelsDetector)(nil)
```

Wire in `evm_chain_specific.go` `labelsDetectors()` after the archive block (78-83):
```go
	if e.hasMethod("debug_proofsSyncStatus") {
		labelsDetectors = append(
			labelsDetectors,
			eth_labels.NewEthHistoricalProofsLabelsDetector(e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector),
		)
	}
```

- [ ] **Step 4: Run** `go test ./internal/upstreams/labels/eth_labels/` → PASS.
- [ ] **Step 5: Commit** `feat(labels): publish historical_proofs from debug_proofsSyncStatus`

---

## Task 6: Method detection and public exposure

**Files:**
- Modify: `internal/upstreams/methods/evm_methods/method_probe_detector.go:22-31`
- Modify: `internal/upstreams/flow/request_processor.go:228` (+ helper) and `request_processor_lower_bound_test.go`

How `debug_proofsSyncStatus` becomes user-callable once `public` ships it (no nodecore code needed):
- `public/pkg/methods/method_groups.go:41-50` indexes every grouped method into `default`; `methods.NewUpstreamMethods` (methods.go:24-73) starts from `default`, so the method is enabled unless an upstream config sets `methods.disable: [debug]` or `[debug_proofsSyncStatus]`. There is no separate allowlist.
- `RpcModulesDetector` (rpc_modules_detector.go:46-68) strips it only when the node does not list the `debug` module (`moduleOf("debug_proofsSyncStatus") == "debug"`). op-reth with `--http.api ...,debug` lists it.
- A node that lists `debug` but is not op-reth (geth) would otherwise advertise the method (rpc_modules is an "unreliable positive", rpc_modules_detector.go:17-21). Fix: probe it.
- Caching: `cache_policies.go:256-263` obeys `method.IsCacheable()`; the v1.2.2 spec entry carries `"settings": {"cacheable": false}` (`eth-json-rpc.json:165-167`), so responses are not cached. No nodecore change.

- [ ] **Step 1: Probe the method** — `method_probe_detector.go:22-31`, append `"debug_proofsSyncStatus",` to `probedMethods` (read-only; op-reth answers a result with empty params → `MethodAvailable`; geth answers `the method debug_proofsSyncStatus does not exist/is not available` → `MethodNotAvailable`). `method_probe_detector_test.go` does not pin the list (grep for `eth_callBundle`/`probedMethods` returns nothing), so no test edit; the probe set is built from the chain spec (`NewMethodProbeDetector` 62-84), which contains the method from public v1.2.2 (Task 0).

- [ ] **Step 2: Do not let above-window failures raise the proof lower bound.** Decided: the guard applies only to upstreams that publish an upper proof bound; an upstream without one (no `debug_proofsSyncStatus`) keeps today's live-correction behaviour unchanged. `request_processor.go:228`:
```go
	if lowerBound, ok := liveLowerBoundFromPrunedError(request.Method(), parsedParam, response, upstream.GetCurrentHeadHeight()); ok && !aboveUpperProofWindow(upstream, lowerBound) {
		upstream.UpdateLowerBound(lowerBound)
	}
```
add after `liveLowerBoundFromPrunedError`:
```go
// upperBoundPredictor is the slice of upstreams.Upstream the guard below needs.
type upperBoundPredictor interface {
	PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64
}

// aboveUpperProofWindow reports whether a live proof-bound correction was derived from a
// block beyond the upstream's historical proof window. Such a failure says nothing about
// the window's lower edge and must not raise it - Same() would then refuse the detector's
// next, lower and correct, value. An upstream that publishes no upper bound (predicted 0)
// keeps the default behaviour.
func aboveUpperProofWindow(upstream upperBoundPredictor, bound protocol.LowerBoundData) bool {
	if bound.Type != protocol.ProofBound {
		return false
	}
	upper := upstream.PredictLowerBound(protocol.UpperProofBound, 0)
	return upper != 0 && bound.Bound-1 > upper
}
```
(`bound.Bound` is `block+1`, request_processor.go:300. `PredictLowerBound(UpperProofBound, 0)` returns 0 for an upstream that never published the upper bound — `LowerBounds.PredictNextBound` at lower_bounds.go:91-95 and `GenericUpstream.PredictLowerBound` at upstream.go:178-190 — so `upper != 0` is the "has such bounds" gate.) No upstream mock in `pkg/test_utils/mocks` implements `PredictLowerBound` (grep), so declare the parameter as the narrow interface `type upperBoundPredictor interface{ PredictLowerBound(protocol.LowerBoundType, int64) int64 }` (declare it in `request_processor.go` next to the helper; `upstreams.Upstream` satisfies it) and test with a local stub `type fixedUpper int64` whose `PredictLowerBound` returns `int64(f)`. Tests in `request_processor_lower_bound_test.go`:
- upper `5000`, `eth_getProof` pruned error at block `6000` → `aboveUpperProofWindow` true (correction skipped);
- upper `5000`, block `4000` → false (correction applied);
- upper `0` (no upper bound published), block `6000` → false (default behaviour, correction applied);
- upper `5000`, a `StateBound` correction → false.
Whether op-reth answers a pruned-shaped error for blocks in `(latest, head]` is `unverified — confirm first`; the guard is correct in both cases.

- [ ] **Step 3: Run** `go test ./internal/upstreams/flow/ ./internal/upstreams/methods/...` → PASS.
- [ ] **Step 4: Commit** `feat(methods): probe debug_proofsSyncStatus; guard proof bound from above-window failures`

---

## Task 7: gRPC mapping

**Files:**
- Modify: `emerald-grpc/proto/blockchain.proto:148`
- Modify: `internal/server/emerald/chain_event_mapper.go:131-156`
- Modify: `internal/server/emerald/selector_mapper.go:124-149`
- Test: `internal/server/emerald/selector_mapper_test.go:95-124`, `internal/server/emerald/chain_event_mapper_test.go:73-103`

Depends on Task 0 (public v1.2.2 provides `dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER`).

- [ ] **Step 1: Mirror the proto copy** — after `    LOWER_BOUND_RECEIPTS = 10;` at `emerald-grpc/proto/blockchain.proto:148` insert exactly these two lines from public v1.2.2:
```
    // upper bound of the historical proof store (op-reth debug_proofsSyncStatus.latest). Reuses the LowerBound message.
    LOWER_BOUND_PROOF_UPPER = 11;
```
Also copy the v1.2.2 comment block above `message LowerHeightSelector` (starts `// Matches nodes whose bound of `lower_bound_type` covers `height`.`) into the nodecore copy so the two files stay in sync; compare with `diff <(curl -s https://raw.githubusercontent.com/drpcorg/public/v1.2.2/proto/blockchain.proto) emerald-grpc/proto/blockchain.proto` and resolve any other drift the same way (take public's text).
- [ ] **Step 2: Failing tests** — add `{name: "proof upper", apiType: dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER, expected: protocol.UpperProofBound}` to the table at `selector_mapper_test.go:101-111`; add `protocol.NewLowerBoundData(120, 1200, protocol.UpperProofBound)` → `{LowerBoundTimestamp: 1200, LowerBoundValue: 120, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER}` to `chain_event_mapper_test.go:73-103`.
- [ ] **Step 3: Implement** — `lowerBoundTypeToApi`: `case protocol.UpperProofBound: return dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER`. `mapDshackleLowerBoundType`: `case dshackle.LowerBoundType_LOWER_BOUND_PROOF_UPPER: return protocol.UpperProofBound, true`.
- [ ] **Step 4: Run** `go test ./internal/server/emerald/ -run 'LowerBound'` → PASS. `go build ./...`.
- [ ] **Step 5: Commit** `feat(grpc): map LOWER_BOUND_PROOF_UPPER both ways`

---

## Rollout and compatibility

- Old aggregator (dproxy before its `LOWER_BOUND_PROOF_UPPER` change) receives `LowerBound{lower_bound_type: 11}`; protobuf-go keeps the numeric value and `pkg/lower_data/lower_data.go:193-194` maps it to `Unknown`, as does `pkg/reducer_states/provider_state.go:810-811`. `Unknown` has a `String()` case (`lower_data.go:84-85`), so nothing panics. `[INFERENCE]` from the read mapping code: the value is stored under `Unknown` and ignored by routing.
- Old dproxy never emits `LOWER_BOUND_PROOF_UPPER` selectors, so the new matcher is unreachable until dproxy changes.
- `historical_proofs=false` appears on every EVM upstream (same footprint as `archive`).
- Chain-level `LowerBoundsToApi` (sub_chain_status.go:226) now carries the chain-wide max `UPPER_PROOF` next to the chain-wide min `PROOF`; a nodecore mixing a proof-store node and a plain node publishes `[min earliest, max latest]`, which is what dproxy needs to pick the provider; the per-node selectors then pick the node.

## Blockers (outside this repo)

- B1 dproxy: `pkg/typedrpc/selectors.go:58-59`, `pkg/lower_data/lower_data.go:169-196`, `services/aggregator/src/server/server.go:276-282` need `LOWER_BOUND_PROOF_UPPER`; not nodecore work, but routing by the upper bound is inert until then.
- B2 dshackle-compatible consumers of `SubscribeStatus`/`NodeStatus` (Kotlin dshackle, `emerald-dshackle` clients): Java protobuf maps enum 11 to `UNRECOGNIZED`; an exhaustive `when` over `LowerBoundType` would throw. `unverified — confirm first` in the dshackle repo (`LowerBoundType` usages) before enabling on a nodecore that serves dshackle clients.
- The `public` side (enum + spec method with `cacheable: false`) is already released in v1.2.2; no blocker remains there.

## Critical files & anchors

- `internal/upstreams/lower_bounds/evm_bounds/evm_lower_bound.go:75-83` — source priority order lives here; sync status goes first.
- `internal/upstreams/lower_bounds/lower_bounds.go:31-58` — the `switch` is where the upper edge must bypass the archive and regression branches.
- `internal/protocol/upstream_state_events.go:34-46` — `Same()` would silently drop every unwind without the upper-edge branch.
- `internal/upstreams/chain_supervisor_state.go:163-180` — min→max for the upper edge; easy to miss because tests pass without it.
- `internal/upstreams/flow/selectors.go:124-128` — upper type must be branched before the `Height == 0` sort-hint case.

## Verification

Unit (per task, already listed): `go test ./internal/protocol/ ./internal/upstreams/lower_bounds/... ./internal/upstreams/flow/ ./internal/upstreams/labels/eth_labels/ ./internal/upstreams/methods/... ./internal/upstreams/ ./internal/server/emerald/` and `go build ./...`.

End-to-end (after Task 7, needs a dRPC op-reth upstream with `--proofs-history --proofs-history.storage-version=v2` reachable from the dev machine; config in `configs/` with that upstream on `optimism`):
1. Start nodecore. Within ~15 s + one detection cycle expect logs `upstream '<id>' lower bound of type PROOF is <earliest>` and `upstream '<id>' lower bound of type UPPER_PROOF is <latest>` (lower_bound_processor.go:83), and `upstream '<id>' label of historical_proofs is true` (label_processor.go:43). No `eth_getProof` probe traffic for that upstream (`countRequests` equivalent: upstream access log).
2. `curl -s -X POST localhost:<port>/optimism -d '{"jsonrpc":"2.0","id":1,"method":"debug_proofsSyncStatus","params":[]}'` → `{"earliest":…,"latest":…}`; repeat after 10 s → `latest` advanced (not served from cache).
3. gRPC: `grpcurl -plaintext -d '{"chain":"CHAIN_OPTIMISM"}' localhost:<grpc-port> emerald.Blockchain/SubscribeStatus` → `lower_bounds` contains `lower_bound_type: 11` with `lower_bound_value == latest` and `LOWER_BOUND_PROOF` with `earliest`.
4. `grpcurl ... emerald.Blockchain/NativeCall` with `selector: {lowerHeightSelector: {height: <latest+100000>, lowerBoundType: 11}}` and method `eth_blockNumber` → error naming `Upstream upper height … of type UPPER_PROOF is less than …` from the proof-store node; with `height: <latest-10>` → served. A plain (non-proof-store) upstream on the same chain is served in both cases.
5. Unwind check (optional, needs operator access): `op-reth proofs unwind --target <latest-1000>` then restart; the next cycle logs `UPPER_PROOF is <latest-1000>` and `SubscribeStatus` reflects the lower value.

## Assumptions & contingencies

- Decided with the user: `historical_proofs=false` is emitted when the method is definitively absent (Task 5); transient errors emit nothing.
- Decided with the user: the above-window live-correction guard (Task 6 Step 2) is included and is gated on the upstream having published an upper proof bound; upstreams without the method keep the default live-correction behaviour.
- `earliest`/`latest` arrive as hex strings or decimal numbers; the parser accepts both. If a live op-reth returns another shape (e.g. nested objects), extend `parseEvmProofsSyncStatus` only; the test fixture format is the single place to update.
