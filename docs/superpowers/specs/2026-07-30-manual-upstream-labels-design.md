# Manual upstream labels

## Goal

Let operators set upstream labels statically in the config, the way dshackle does, instead of relying only on runtime label detectors. As part of this, retire `options.archive` (`chains.Options.ArchiveCapability`) and express the same intent through a manual `archive` label.

## Semantics

Manual labels are **startup seeds**:

- They are written into `protocol.UpstreamState.Labels` when the upstream is constructed, so they are visible to label matchers, `client_version`-style lookups, and the gRPC `AggregatedLabels` immediately — including when `disable-labels-detection` is on.
- Runtime detectors **overwrite** them. A detector that owns a key wins over the manual value for that key, on its first detection round.
- The only exception is an explicit per-detector opt-out. Today there is exactly one: the EVM archive detector is not started when the manual label `archive` is `"false"`, so the seeded value stands for the process lifetime.

`labels` is independent of the existing per-upstream `group-labels`: `group-labels` is config-only input to `label-balancing` and is never published into upstream state. Manual labels do **not** feed `label-balancing`.

## Config

`internal/config/upstream_config.go`, on `Upstream`:

```go
Labels UpstreamLabels `yaml:"labels"`
```

```yaml
upstreams:
  - id: full-node
    chain: ethereum
    labels:
      archive: false        # unquoted booleans and numbers are fine
      provider: hetzner
      min-peers: 3
    connectors:
      - type: json-rpc
        url: https://full-node.example.com
```

Label values are strings in Go, but any YAML **scalar** is accepted and stored as its literal text, the way dshackle accepts a bare `true`/`false`. A plain `map[string]string` cannot do this — yaml.v3 rejects a `!!bool` node with `cannot unmarshal !!bool ... into string` — so the field uses a named type with its own unmarshaller:

```go
// UpstreamLabels is a manual label map. Any YAML scalar value is accepted and
// stored as its literal text, so `archive: false` and `archive: "false"` are
// equivalent.
type UpstreamLabels map[string]string

func (l *UpstreamLabels) UnmarshalYAML(node *yaml.Node) error
```

The unmarshaller walks the mapping node's `Content` pairs and:

- requires a mapping node (`labels must be a mapping of label names to scalar values`)
- requires each value to be a scalar (`label '<key>' must have a scalar value`) — sequences and nested maps are rejected
- resolves YAML aliases before the kind checks, both for the `labels` node itself and for each value, so a shared labels block can be factored out with an anchor and reused; merge keys (`<<`) remain unsupported but report that explicitly rather than blaming a label named `<<`
- stores `value.Value`, so `false` → `"false"`, `3` → `"3"`, `"false"` → `"false"`, and a null value (`archive:` with nothing) → `""`, which the validation below then rejects
- rejects a duplicate label key, matching what yaml.v3 does when decoding into a plain map (node walking bypasses that check)

It is named `UpstreamLabels` rather than `Labels` because `internal/upstreams/upstream.go` imports both `config` and `protocol`, and `protocol.Labels` already exists.

Validation in `Upstream.validate()`, next to the existing `GroupLabels` loop:

- empty key → `labels must not contain an empty key`
- empty value → `label '<key>' must have a non-empty value`

No reserved keys and no key-format rules: detectors are allowed to own any key, and manual labels are free-form.

## Removing `options.archive`

`ArchiveCapability` is deleted from `pkg/chains/options.go`. It was settable both per-upstream (`upstream.options.archive`) and per-chain (`chain-defaults.<chain>.options.archive`); the replacement is per-upstream `labels` only. There is no chain-defaults-level `labels` map.

Config parsing uses non-strict `yaml.Unmarshal`, so a leftover `options.archive` is silently ignored rather than rejected — an upstream that relied on `archive: false` starts running archive detection again. This is documented as a migration note; no compatibility shim or deprecation validator is added.

## State seeding

`NewBaseUpstream` fills the `Labels` object that `DefaultUpstreamState` already created, before the state is published:

```go
state := protocol.DefaultUpstreamState(...)
for key, value := range conf.Labels {
    state.Labels.AddLabel(key, value)
}
upState.Store(state)
```

No new `protocol` constructor and no second `Labels` object. `DefaultUpstreamState`'s signature is also deliberately unchanged (it has five positional parameters and ~15 call sites, all in tests). Mutating the freshly built state before `Store` is safe because nothing else holds a reference yet.

`NewBaseUpstreamWithParams` receives an already-built `upState` from its caller and is **not** changed — seeding there would stomp the caller's intent.

## Archive detector

`evm_specific.NewEvmChainSpecific` gains a `manualLabels map[string]string` parameter, passed as `conf.Labels` from `getChainSpecific` in `internal/upstreams/upstream_factory.go`, and stored on `EvmChainSpecificObject`.

`tron_specific.NewTronSpecific` gains the same final parameter and forwards it on its `json-rpc` branch, which delegates to `NewEvmChainSpecific` and therefore shares the EVM label detectors. Its `rest` branch (`newTronRestSpecific`) needs nothing — `TronRestSpecific.LabelsProcessor` has no archive detector. Without this, `archive: false` would silently stop working on a path where `options.archive: false` used to work.

`archiveLabelsDetector` is removed. The detector list moves into a private method so the decision is directly testable, and `LabelsProcessor()` becomes a one-liner over it:

```go
func (e *EvmChainSpecificObject) LabelsProcessor() labels.LabelsProcessor {
	return labels.NewBaseLabelsProcessor(e.ctx, e.upstreamId, e.labelsDetectors(), e.options.ValidationInterval*5)
}

func (e *EvmChainSpecificObject) labelsDetectors() []labels.LabelsDetector {
	// ... the five unconditional detectors ...
	if !archiveDetectionSuppressed(e.manualLabels) {
		labelsDetectors = append(labelsDetectors, eth_labels.NewEthArchiveLabelsDetector(
			e.upstreamId, e.chain.Chain, e.options.InternalTimeout, e.connector,
		))
	}
	return labelsDetectors
}

// archiveDetectionSuppressed reports whether the manual 'archive' label pins the
// value to false, in which case the runtime archive probe must not run.
func archiveDetectionSuppressed(manualLabels map[string]string) bool {
	return manualLabels["archive"] == "false"
}
```

The match is exact-string `"false"`; `"False"`/`"FALSE"` do not suppress detection. `archive: "true"` (like the old `options.archive: true`) does not suppress it either — the detector runs and may overwrite the seeded value with its detected result.

`internal/upstreams/labels/static_labels_detector.go` is deleted: the archive path was its only caller, and the seeded state now covers that case without a detector that republishes a constant every interval.

## Docs

`docs/nodecore/05-upstream-config.md`:

- New `labels` entry in the per-upstream **Fields** section: seeded at startup, overwritten by detectors that own the same key, exception for `archive: false` (stating that the match is exact and case-sensitive, so `archive: False` does not suppress anything), values may be written as bare scalars, and an explicit contrast with `group-labels`.
- Remove the `options.archive` bullet and the `archive: false` line from `chain-defaults.<chain>.options` in the top-level example. The example upstream gets an inert `labels` block (`provider: hetzner`) rather than `archive: false`, so copy-pasting the canonical example cannot silently disable archive detection; the `archive: false` example lives in the `labels` field entry and its migration note.
- Note in the EVM label-detectors row of the **Validators and labels** table that the archive detector is skipped when the upstream's manual `archive` label is `false`.
- Migration note: `options.archive` is gone and silently ignored if left in place, and `labels` is per-upstream only — a `chain-defaults.<chain>.labels` key is likewise silently ignored.

## Tests

**No test may use `reflect` to read a private field.** Where a behavior is only observable through unexported state, extract a private method and test that method — which is why `labelsDetectors()` exists as its own method above.

- `internal/config`: `UpstreamLabels` unmarshalling — unquoted `false`/`3` and quoted `"false"` all become their literal text; sequence/map values rejected; non-mapping `labels` rejected; duplicate key rejected with the exact message `duplicate label '<key>'`; empty key and empty (null) value rejected by `validate()`; aliases resolved (alias to a scalar value, alias as the whole `labels` mapping) and merge keys reported intelligibly.
- `internal/upstreams`: manual labels are present in the state produced by `NewBaseUpstream`, including when `disable-labels-detection` is `true`; a detector event for the same key replaces the seeded value. The overwrite test asserts the seeded value is in place **before** applying the event, so it fails if the seeding is removed.
- `internal/upstreams/chains_specific/evm_specific`: replace `archive_labels_detector_test.go` with (a) a test over the suppression predicate — suppressed only for exactly `"false"`, not for `"true"`, `"False"`, `""`, an unrelated label, or no labels; (b) a test that `NewEvmChainSpecific` keeps the manual labels it is handed; (c) a same-package test calling `labelsDetectors()` and asserting an `*eth_labels.EthArchiveLabelsDetector` is absent from the returned slice for `archive: "false"` and present for nil / `"true"` / `"False"`. Presence/absence rather than a count, so adding an unrelated detector later does not break it.
- Tron: the `json-rpc` dispatch is covered by the existing `TestNewTronSpecificDispatchesJsonRpcToEvm`. The forwarded label value is **not** unit-asserted — `EvmChainSpecificObject.manualLabels` is unexported in another package, and reaching it would require reflection, which is banned. The suppression contract itself is covered by the `evm_specific` test above.
- `test/e2e/http/archive_selector_e2e_test.go`: switch its nodecore config from `options.archive` to upstream `labels`. Note that with seeding in place this test no longer exercises the archive *detector* — both upstreams carry their label before any probe runs — so it validates manual label → gRPC selector → routing, and the detector contract is covered at unit level.

## Non-goals

- Chain-defaults-level or global label maps.
- Manual labels participating in `label-balancing`.
- Per-detector opt-outs beyond archive.
- A compatibility shim for `options.archive`.
