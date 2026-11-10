# Compatible-client rules: family-wide validation with hot reload

**Date:** 2026-11-10
**Branch:** `validate_clients`
**Status:** design agreed in discussion, ready for an implementation plan

## Problem

`drpcorg/public` ships `compatible-clients.yaml`: a list of node clients with the versions
that must not serve traffic (a blacklist, optionally a whitelist), optionally scoped to a set
of networks. nodecore embeds a copy of that file through the pinned `github.com/drpcorg/public`
module and checks it with `EthClientVersionValidator`, but:

- the rules only change on a dependency bump and a redeploy, so a version blacklisted today is
  served until the next release;
- the check exists only for EVM upstreams, although every chain family but Polkadot and
  Celestia already detects a client type and version: 18 of them through a
  `ClientLabelsDetector` wrapped in `ClientLabelsDetectorHandler`, Starknet through a
  standalone two-probe detector;
- the whitelist is ignored;
- the rule list is read as a flat sequence, and a rule written for one network is checked
  next to the client's general rule with no defined relation between the two.

## Goals

1. Rules are refreshed at runtime from a configurable URL (default: the raw file on the
   `main` branch of `drpcorg/public`), seeded from the embedded copy, with the last good rule
   set kept on any failure.
2. One generic settings validator checks every upstream whose chain family has a client
   detector; an upstream running a forbidden version is refused at startup and removed at
   runtime, exactly like any other fatal settings error, and re-admitted as soon as a later
   check is clean.
3. Rules are indexed per client: a rule without `networks` is the client's default, a rule with
   `networks` extends that default on those networks.
4. Whitelists are honoured.

## Non-goals

- `excluded-methods` in the file stays ignored.
- The file itself is not corrected here. The bsc reth entry written as a full raw version
  string never matches a parsed version; it stays as it is.
- No change to how labels are detected or published; the labels processor is untouched.
- No metrics for the registry.

## Design

### 1. Rule index: package `internal/upstreams/compatible_clients`

A new package owns everything about the rules: the YAML structs, the index, the lookup, the
version matching, the default URL and the embedded seed.

The YAML shape is the file's: `rules: [{client, networks?, blacklist?, whitelist?}]`, decoded
with `gopkg.in/yaml.v3` into `Rules{Rules []Rule}` / `Rule{Client, Networks, Blacklist,
Whitelist}`; unknown keys are ignored.

`Rules` builds its index **during unmarshalling**: it implements `yaml.Unmarshaler` with the
standard alias idiom, so any `yaml.Unmarshal` into a `Rules` (the holder's refresh, the
embedded seed, a test) yields a value that is ready to look up, and the holder (§2) needs no
notion of initialization:

```go
func (r *Rules) UnmarshalYAML(node *yaml.Node) error {
    type plain Rules                      // same fields and tags, no methods: no recursion
    if err := node.Decode((*plain)(r)); err != nil {
        return err
    }
    return r.buildIndex()
}
```

`buildIndex` fills unexported fields. The index is keyed by lowercased client. Each client
entry holds one **default rule** (built from the entries with no `networks`, or an empty list)
and one rule per lowercased network name; an entry listing several networks is indexed under
each of them. Two entries landing on the same key merge: their blacklists are unioned and
their whitelists are unioned. Every version string is normalized once at index time (see §3).
`Rules` is only ever loaded as YAML, so there is no `UnmarshalJSON`.

A constructor decodes the embedded `public.GetCompatibleClients()` into a `*Rules`; `app.go`
uses it as the initial value (§6). The default URL constant lives here too.

Still at decode time, every network rule is extended by the client's default rule, so the
stored rules are final and a check never merges:

- **blacklist** = default ∪ network;
- **whitelist** = the network rule's whitelist if it is non-empty, else the default's.

`(*Rules).Check(clientType, clientVersion string, chain *chains.ConfiguredChain) Verdict` is
the one read-path method (the user asked for a single call after implementation; the earlier
`Lookup` + `EffectiveRule` pair is gone). The chain's names are its `Chain.String()` followed
by its `ShortNames`, lowercased; the rule applied is the one for the first of those names that
has an entry, else the client's default rule. `Verdict` is `NoRule`, `Allowed`,
`NotWhitelisted` or `Blacklisted` (whitelist is judged first, see §3).

The asymmetry is deliberate. A blacklist only forbids, so extending it is safe. A whitelist
only permits, so a union would make a network rule looser than the default and leave no way to
write "on this network only these versions". With "most specific non-empty whitelist wins"
both readings stay expressible: list the defaults again to extend, list fewer to narrow.

A client with no entry, or an entry with no rule for the chain and no default, is `NoRule`.

### 2. Reloadable value

A small generic in `pkg/utils`, the same shape as the richer implementation it is modelled
on:

```go
type Format int

const (
    JSON Format = iota
    YAML
)

type Reloadable[T any] struct { … }

// NewReloadable returns the holder seeded with initial and starts the refresh loop on ctx.
func NewReloadable[T any](
    ctx context.Context,
    initial *T,
    source string,
    format Format,
    period time.Duration,
) *Reloadable[T]

func (r *Reloadable[T]) Get() *T
```

- `T` is unconstrained. A refresh decodes into a fresh `new(T)` according to `format`
  (`sonic` for JSON, `yaml.v3` for YAML) and stores the pointer. Anything a type needs to
  derive from its fields it does in its own `UnmarshalYAML` / `UnmarshalJSON` hook, so the
  holder never knows about initialization; `Rules` does exactly that (§1).
- `source` starting with `http://` or `https://` is fetched with a plain GET (a fresh request
  per refresh, 10s timeout, only `200` accepted); anything else is read as a local file path.
- The loop runs on `ctx`: the first refresh starts right away in the background, then one per
  `period`. Startup never waits for the network: readers see `initial` until the first
  successful decode.
- The bytes of the last successful fetch are hashed; identical content is a Debug line and
  no decode. Changed content that decodes is stored atomically and logged once at Info.
- A fetch or decode failure keeps the current value. It is logged at Warn on the first failure
  after a success (or after start), and at Debug while it keeps failing, so an offline
  deployment does not warn every period.
- Readers never mutate what `Get()` returns; a new value replaces it whole.

Compared with the implementation this is modelled on, there is no S3, no bearer token, no
environment substitution, no subscribers, no post-load callback (the unmarshal hook is the
hook) and no blocking retry at startup.

### 3. The validator

`validations.ClientVersionValidator` (new file in `internal/upstreams/validations`) replaces
`eth_validations.EthClientVersionValidator`, which is deleted with its tests.

Construction takes the upstream id, the configured chain, the family's
`labels.ClientDetector` (the `LabelsDetector` plus `DetectClient`, implemented by
`ClientLabelsDetectorHandler`; the specifics return it from `ClientLabelsDetector()`) and the
rule holder
(`*utils.Reloadable[compatible_clients.Rules]`, read on **every** `Validate(ctx)` through
`Get().Check(...)`). No import cycle: `compatible_clients` depends on `chains`, `utils` and
`public` only.

`ClientLabelsDetectorHandler` gains:

```go
// DetectClient performs the node-type request and returns the parsed client type and
// version. DetectLabels is built on it.
func (c *ClientLabelsDetectorHandler) DetectClient(ctx context.Context) (clientType, clientVersion string, err error)
```

so the validator can tell a failed probe from a node that reports nothing. `DetectLabels`
keeps its behaviour (nil map and an Error log on failure).

The handler also learns to hold **one or more probes**: `NewClientLabelDetectorHandler`
accepts a list of `ClientLabelsDetector` (the 18 existing callers pass their single one, the
exact constructor shape is the builder's). `DetectClient` tries them in order under one
`internalTimeout` each; a probe whose request comes back as an error answer falls through to
the next (Debug log), a successful answer is parsed by that probe and returned, and when no
probe answers the last error is returned. With a single probe this is exactly today's
behaviour. The multi-probe form exists for Starknet (§4).

`Validate(ctx)`:

1. `DetectClient(ctx)`, once, no retry (the user dropped the retry after implementation: the
   next tick is the retry). Any error → `SettingsError` (at startup the upstream is not
   admitted until a later tick answers; at runtime a `SettingsError` is a no-op). This is the
   same rule every other settings validator follows: a node that does not answer its version
   probe is not checked, and in strict mode not admitted.
2. Empty client type or version → `Valid`: there is nothing to match against. The EVM
   detector's `unknown` values fall under the same rule, since no entry is written for them.
3. `rules.Get().Check(clientType, clientVersion, chain)` (§1). `NoRule` → `Valid`.
4. Non-empty whitelist that does not contain the version → `FatalSettingError`.
   Version in the blacklist → `FatalSettingError`. Otherwise `Valid`.
   Fatal logs the upstream, client, version, chain and the offending list at Warn.

**Version matching.** Both sides are normalized: trim, drop one leading `v`, lowercase. The
label additionally gets a *core*: everything before the first `-` or `+`. A rule entry matches
the label when it equals the normalized label or equals the label's core. Rules are never
prefixes: `1.4` does not match `1.40.0`.

| Node reports | Label | Core | `v1.4.0` | `v1.4.0-5e4e5c6` |
|---|---|---|---|---|
| `reth/v1.4.0-5e4e5c6/x86_64-unknown-linux-gnu` | `v1.4.0-5e4e5c6` | `1.4.0` | match | match |
| `reth/v1.4.0-9a1b2c3/…` | `v1.4.0-9a1b2c3` | `1.4.0` | match | no |
| `erigon/2.40.0/linux-amd64/go1.19` | `2.40.0` | `2.40.0` | `v2.40.0` matches | |
| `Nethermind/v1.25.4+20b10b35/linux-x64/dotnet8` | `v1.25.4+20b10b35` | `1.25.4` | `1.25.4` matches | |
| `Geth/v1.13.14-stable-2bd6bd01/linux-amd64/go1.21` | `v1.13.14-stable-2bd6bd01` | `1.13.14` | `v1.13.14` matches | |

A bare version in the file means every build of that release; a version with a suffix means
exactly that build. The raw response string is never compared.

**Worked rules**, using the live file plus a hypothetical whitelist pair:

```yaml
rules:
  - client: reth
    blacklist: [v1.4.0, v1.4.1, v1.4.2]
  - client: reth
    networks: [bsc]
    blacklist: ["reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu"]
  - client: erigon
    networks: [ethereum]
    blacklist: [v2.40.0, "3.1.0"]
  - client: besu                      # hypothetical
    whitelist: [v24.1.0, v24.1.2]
    blacklist: [v23.10.0]
  - client: besu                      # hypothetical
    networks: [polygon]
    whitelist: [v24.3.0]
    blacklist: [v24.1.0]
```

| Upstream | Effective blacklist | Effective whitelist | Examples |
|---|---|---|---|
| reth on ethereum | 1.4.0, 1.4.1, 1.4.2 | none | `v1.4.1-abc` Fatal, `v1.5.0-abc` Valid |
| reth on bsc | 1.4.0, 1.4.1, 1.4.2, `reth/v1.6.0-…` | none | `v1.4.0-xyz` Fatal, `v1.6.0-2a4968e` Valid |
| erigon on ethereum | 2.40.0, 3.1.0 | none | `2.40.0` Fatal |
| erigon on polygon | no rule | | `2.40.0` Valid |
| geth anywhere | no rule | | always Valid |
| besu on ethereum | 23.10.0 | 24.1.0, 24.1.2 | `v24.1.2` Valid, `v24.2.0` Fatal, `v23.10.0` Fatal |
| besu on polygon | 23.10.0, 24.1.0 | 24.3.0 | `v24.3.0` Valid, `v24.1.2` Fatal, `v24.1.0` Fatal |

### 4. Chain specifics

`chains_specific.ChainSpecific` gains:

```go
// ClientLabelsDetector returns the handler that probes the node's client type and
// version, or nil for a family that cannot ask. The factory builds the client-version
// validator on it.
ClientLabelsDetector() labels.ClientDetector
```

Every specific that already builds a `NewClientLabelDetectorHandler(...)` inside
`LabelsProcessor()` returns the same construction here (building it once in the constructor
and sharing it is fine; so is constructing it in both places, the handler is stateless). The
specifics without one (Polkadot, Celestia) return nil. The flavor pickers (tron, cosmos,
stellar, ton) already return a concrete specific, so nothing forwards.

**Starknet** is rewritten the normal way. `starknet_labels.StarknetClientLabelsDetector`
(a standalone `LabelsDetector` with its own request loop) is replaced by two plain
`ClientLabelsDetector`s: one whose `NodeTypeRequest` is `pathfinder_version` and whose
`ClientVersionAndType` returns the quoted bare version and `pathfinder`, one for
`juno_version` and `juno`. The version keeps today's shape (leading `v` stripped, so the
label does not change). `StarknetChainSpecificObject` builds one multi-probe handler over the
two, in that order, and uses it in both `LabelsProcessor()` and `ClientLabelsDetector()`. A
node that answers neither probe yields no labels and a `SettingsError` from the validator,
like any other family whose probe fails. The docs row for the Starknet client label detector
keeps its meaning.

`EvmChainSpecificObject.SettingsValidators()` drops the `ValidateClientVersion` branch.

### 5. Config

A new section under `upstream-config`:

```yaml
upstream-config:
  compatible-clients:
    enabled: true          # default: true in strict mode, false in default mode
    url: https://raw.githubusercontent.com/drpcorg/public/main/compatible-clients.yaml
    refresh-interval: 10m
```

- `enabled` follows the mode like the option it replaces: `true` in `strict`, `false` in
  `default`. `enabled: false` means no registry, no fetching and no validator on any upstream.
- `url` defaults to the raw file on `main`; any value accepted by `Reloadable` (an `http(s)`
  URL or a local path). Validation: non-empty when enabled.
- `refresh-interval` defaults to `10m`; must be greater than zero.

The section is restart-only. `RestartOnlyChanges` reports it by reflection without any change.

`validate-client-version` is removed from `chains.Options`, `setOptionsDefaults`, the per-mode
table and the option list in `docs/nodecore/05-upstream-config.md`, and the example config at
the top of that document. YAML decoding here is not strict, so a config that still sets it is
ignored, not rejected.

### 6. Wiring

- `internal/app/app.go` builds the registry when the section is enabled: the initial value is
  the embedded seed from `compatible_clients` (a decode failure there is a startup error: the
  file is our own and validated upstream), then `utils.NewReloadable(ctx, initial, url,
  utils.YAML, interval)`. Disabled → nil.
- The registry is passed to `NewGenericUpstreamSupervisor` and on to `CreateUpstream`, which
  hands it to `createSettingValidationProcessor`. That function appends the
  `ClientVersionValidator` when the registry is non-nil and `chainSpecific.ClientLabelsDetector()`
  is non-nil, after the family's own validators. The existing `DisableValidation` /
  `DisableSettingsValidation` gates keep applying.
- A reload of the upstream list builds new upstreams through the same factory, so they get the
  validator too.

### 7. Docs

`docs/nodecore/05-upstream-config.md`:

- a `## compatible-clients` section after `## mode` describing the three fields, the file
  format, the default/extension rule, the whitelist rule, version matching and the refresh
  behaviour (keep-last-good, embedded seed);
- the per-mode table gets a `compatible-clients.enabled` row in place of
  `validate-client-version`;
- the validators table in `## Validators and labels` gets the client-version row for every
  family with a client detector.

## Tests

- **Index** (`compatible_clients`, through `yaml.Unmarshal` + `Check`, asserting verdicts): default-only,
  network-only, default + network (blacklist union, whitelist precedence both ways),
  duplicates merge, several networks on one entry, case-insensitive client and network, chain
  matched by short name, unknown keys ignored, empty `rules`; the embedded seed decodes to a
  ready index.
- **Version matching**: the table in §3, plus "no prefix" (`1.4` vs `1.40.0`) and a suffixed
  rule not matching another build.
- **Validator** (mock connector): forbidden version → Fatal; allowed → Valid; whitelist miss →
  Fatal; no rule → Valid; probe error → SettingsError after a single probe; empty client →
  Valid; a rule change in the holder is seen on the next `Validate` without rebuilding the
  validator.
- **Handler**: `DetectClient` with one probe (answer, error answer, transport error); with two
  probes the first error answer falls through, the second answer wins, two error answers
  return the last error; `DetectLabels` unchanged.
- **Starknet detectors**: pathfinder answers → `pathfinder` + version without `v`; juno
  answers after a -32601 → `juno`; neither → no labels. The existing Starknet detector tests
  are rewritten against the handler.
- **Reloadable** (a small test struct, local file as source plus an `httptest` server, both
  formats): initial value served before the first fetch; updated value after a change;
  unchanged content not re-decoded; bad content keeps the previous value (including an error
  returned by the type's unmarshal hook); non-200 keeps the previous value; loop stops with
  the context.
- **Config**: mode-dependent default of `enabled`; validation of `url` and
  `refresh-interval`; the section reported as restart-only by `RestartOnlyChanges`.
- **Factory**: validator present for a family with a detector and absent when the registry is
  nil or the family has none.

## Left to the builder

- Exact names of the index fields and config types, and the log wording.
- Whether specifics build the handler once or on each accessor call.
- The HTTP client used by `Reloadable` (a bare `http.Client` with a timeout is enough; no
  pooling concerns at one request per ten minutes).
