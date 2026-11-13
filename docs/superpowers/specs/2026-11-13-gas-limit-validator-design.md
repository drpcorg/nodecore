# Gas-limit validator, creation-call gas probe and `strict-provider` mode

**Date:** 2026-11-13
**Branch:** `gas_validator`
**Status:** design agreed in discussion, waiting for approval before an implementation plan

## 1. Problem

nodecore reports the `eth_call` gas budget of an EVM upstream through the labels `gas-limit` and
`extra_gas_limit`, set by `EthGasLabelsDetector`
(`internal/upstreams/labels/eth_labels/eth_gas_detector.go`). The current probe is wrong in two
ways:

- It installs a contract through a state override. Several clients reject or ignore state
  overrides, so the probe returns nothing on them.
- It sends `eth_call` without `gas`, so it reads the node's default budget, not its cap. Clients
  whose default is lower than their cap are reported as having no extra gas although they grant
  600M when asked.

Besides, nothing removes an upstream whose node has no extra gas. A provider can run a node
with a low `eth_call` gas cap and nodecore serves it anyway.

## 2. Goals and non-goals

Goals:

1. The basic path of the gas label detector asks for 600M explicitly with a contract-creation
   `eth_call` and reads back what the node granted.
2. A new EVM settings validator uses the same probe and removes an upstream that grants less
   than the extra-gas threshold.
3. A new upstream mode `strict-provider` behaves as `strict` and additionally turns this
   validator on by default.
4. Chains whose client cannot grant 600M, or cannot be measured, are exempt through a flag in
   `chains.yaml`.

Non-goals:

- The Monad label path (`monadGasDetect`) is not changed.
- No per-chain thresholds. The threshold is the existing label rule, for every chain.
- No fallback to the old state-override probe.
- No dry-run or report-only phase, and no metric for the validator.
- No guard that keeps a chain alive when every one of its upstreams fails the check.

## 3. Key decisions

| # | Decision | Source |
|---|---|---|
| 1 | Probe: `eth_call` with `{"data":"0x5a60005260206000f3","gas":"0x23C34600"}` at `latest`, no `to`, no state override | agreed |
| 2 | The probe asks for exactly 600,000,000. Never more: some clients reject anything above | agreed |
| 3 | Granted gas = raw result + 53,124. Extra gas = granted above 590,000,000, the existing label rule | agreed |
| 4 | A raw result of 600,000,000 or more is not a gas reading | agreed |
| 5 | Validator verdicts: extra gas is `Valid`; a clean reading below the threshold is `FatalSettingError`; no reading is `SettingsError` | agreed |
| 6 | Option `disable-gas-limit-validation`. Default `false` only in `strict-provider`, `true` in `strict` and `default` | agreed |
| 7 | Exempt chains carry `disable-gas-limit-validation: true` at protocol level in `chains.yaml` (21 protocols, shipped in `drpcorg/public` PR #298) | agreed |
| 8 | `strict-provider` is `strict` in every other respect | agreed |
| 9 | One probe function used by both the detector and the validator; each sends its own request | my call |
| 10 | The validator runs on every validation tick, one request, no retries | my call |
| 11 | The probe lives in a new package `internal/upstreams/gas_limit` | my call |

### Approaches considered for sharing the check

- **One probe function, two callers (chosen).** The detector and the validator call the same
  function and apply the same threshold predicate, so they cannot disagree on the rule. Each
  sends its own request on its own schedule.
- **The validator reads the detector's labels.** One request less, but the labels processor
  and the settings processor run on different schedules and can be disabled independently
  (`disable-labels-detection`), so the validator would depend on a detector that may be off.
- **Two independent implementations.** Rejected: the request, the constant and the sanity rule
  would drift apart.

## 4. Design

### 4.1 The probe: package `internal/upstreams/gas_limit`

The 9 bytes of init code are `GAS PUSH1 0 MSTORE PUSH1 32 PUSH1 0 RETURN`: the call creates a
contract whose constructor returns the gas left. No contract has to exist on the chain.

One exported function sends the request and returns the granted gas or an error that says why
there is no reading:

```go
// Measure asks the node for a 600M eth_call budget and returns what it granted.
// An error means the node gave no usable reading; it says nothing about the node's cap.
func Measure(ctx context.Context, connector connectors.ApiConnector, chain chains.Chain, timeout time.Duration) (*big.Int, error)

// GrantsExtraGas reports whether a granted budget counts as extra gas.
func GrantsExtraGas(granted *big.Int) bool
```

`Measure` returns an error when:

- the request cannot be built or the response carries an error (JSON-RPC error, transport
  error, timeout);
- the result is not a hex number, including the empty `0x`;
- the raw result is 600,000,000 or more. A node that honours the request answers 599,946,876,
  and a node with a lower cap answers less, so such a value is an echo of the request or not a
  gas value at all.

Otherwise it returns raw + 53,124 as a `big.Int`. `GrantsExtraGas` is `granted > 590,000,000`,
the comparison the detector uses today.

The request gas, the creation overhead and the threshold are private constants of this
package. The method name is passed as a literal.

### 4.2 Label detector

`basicGasDetect` calls `gas_limit.Measure`:

- error: log it and return no labels, as today when the probe fails;
- reading: `gas-limit` = granted; `extra_gas_limit` = `600000000` when `GrantsExtraGas`,
  otherwise the granted value.

`DetectLabels` and `monadGasDetect` keep their logic. `gasProbeContract` and `gasProbeBytecode`
stay, used only by the Monad path. On Monad the creation call returns an error, so the detector
falls through to the Monad path exactly as it does today when the basic path yields no 600M
label.

Consequences:

- `gas-limit` now means "what the node granted when asked for 600M", so it tops out at about
  600M. Before, it was the node's default budget. Nothing in nodecore reads either label.
- Clients that return `0x` to the creation call lose the `gas-limit` label they got from the
  old probe. They never had the extra-gas label.

### 4.3 Validator: `EthGasLimitValidator`

New file `internal/upstreams/validations/eth_validations/eth_gas_limit_validator.go`,
implementing `validations.SettingsValidator`:

| `Measure` result | Verdict | Effect |
|---|---|---|
| reading, `GrantsExtraGas` | `Valid` | upstream admitted or re-admitted |
| reading below the threshold | `FatalSettingError` | upstream refused at start, removed at runtime |
| error | `SettingsError` | at start the upstream is not admitted until a later tick is `Valid`; at runtime its state is kept |

It sends one request per validation tick, bounded by `internal-timeout`, without the retrying
`ValidatorExecutor`: a failed call is a `SettingsError` and the next tick asks again.

A fatal verdict logs an error that names the upstream, the granted amount and the required
600M, so the operator knows to raise the node's RPC gas cap.

Wiring in `EvmChainSpecificObject.SettingsValidators()`, with the main connector, next to the
Erigon bug validator:

```go
if e.options.DisableGasLimitValidation != nil && !*e.options.DisableGasLimitValidation {
    settingsValidators = append(settingsValidators, eth_validations.NewEthGasLimitValidator(...))
}
```

### 4.4 Option `disable-gas-limit-validation`

- `chains.Options` gets `DisableGasLimitValidation *bool` with the yaml key
  `disable-gas-limit-validation`.
- `setOptionsDefaults` resolves it like the other flags: upstream option, then
  `chain-defaults`, then the chain's `chains.yaml` settings, then the mode fallback. The
  fallback is `false` in `strict-provider` and `true` otherwise.

So a chain flagged in `chains.yaml` stays exempt in `strict-provider`, and an operator on
`strict` or `default` can still switch the check on for a chain or one upstream.

### 4.5 Mode `strict-provider`

- `StrictProviderMode UpstreamMode = "strict-provider"`, accepted by `UpstreamMode.Validate`.
- `UpstreamMode` gets one method that answers "is this a strict profile" for both `strict` and
  `strict-provider`. Every place that compares with `StrictMode` today uses it instead:
  the option fallbacks in `setOptionsDefaults`, `IntegrityConfig.setDefaults`,
  `CompatibleClientsConfig.setDefaults`, `GetDispatchOptions`, `ValidateLagFor`.
- `GetBestConnector` picks the most capable connector for `strict-provider` as for `strict`.
- `getDefaultPollInterval` already treats everything but `default` as strict.

The only place that tells the two modes apart is the fallback of
`disable-gas-limit-validation`.

### 4.6 `drpcorg/public` dependency

PR #298 adds `disable-gas-limit-validation: true` under `settings.options` of 21 protocols and
the key to the schema. Pin its head commit (`c0804863c87e`) with
`go get github.com/drpcorg/public@<commit>`, then `go mod tidy` and `make generate-networks`.
Once the PR is merged and tagged, the pin moves to the tag.

### 4.7 Docs

`docs/nodecore/05-upstream-config.md`:

- the mode section: a `mode: strict-provider` entry and the per-mode table;
- the options list: `disable-gas-limit-validation`, with the three verdicts;
- the validators table: a row for the gas-limit validator;
- the label detectors row: the gas labels now come from the creation call.

Plus any other doc that lists the valid `mode` values.

## 5. Edge cases

- **Fatal at start.** As for every fatal settings validator, an upstream refused at start is
  not retried until nodecore restarts or reloads its config. A node removed at runtime is
  re-admitted by the next `Valid` tick.
- **A client that rejects instead of clamping.** It never produces a reading, so on a chain
  without the flag it is never admitted in `strict-provider`. Such chains need the flag.
- **An operator overrides the flag to `false` on an exempt chain.** The explicit option wins
  and the validator runs; on a chain that cannot be measured no upstream is admitted.
- **Chains with a different creation overhead.** A few chains charge slightly more or less
  than 53,124 up front. The difference is a few hundred gas and never crosses the threshold.
- **`disable-settings-validation` / `disable-validation`.** They remove the settings processor
  and with it this validator, as for the others.
- **Other settings validators.** The processor reports the worst verdict of all validators, so
  a `SettingsError` from this one holds back an otherwise valid upstream at start.

## 6. Testing

- `gas_limit`: a table over the response shapes: honoured request, low cap, `0x`, non-hex,
  exactly 600,000,000, a value far above the request, JSON-RPC error; plus the request shape
  (no `to`, no state override, exact gas).
- Detector: the existing tests move to the new request; cases for a reading above and below the
  threshold and for no reading. The Monad tests keep passing unchanged in behaviour.
- Validator: the three verdicts.
- Config: `strict-provider` validates; an unknown mode still fails; the option fallback per
  mode; an explicit value and a `chains.yaml` value each beat the fallback; `strict-provider`
  matches `strict` for connector choice, poll interval, dispatch, lag validation, integrity
  and compatible-clients.
- EVM wiring: the validator is present only when the option resolves to `false`.

## 7. Open questions

- The 12 chains where every node sits on the client's default cap are not exempt. In
  `strict-provider` they lose every upstream until their nodes are reconfigured. The chain list
  gets a final review before release.
