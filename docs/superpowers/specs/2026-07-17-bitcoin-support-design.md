# Bitcoin family support — design

Date: 2026-07-17
Status: approved (pre-implementation)

## Goal

Add first-class support for the **Bitcoin** blockchain family to nodecore
(`BlockchainType = "bitcoin"`), covering **bitcoin** and **dogecoin** (mainnet
and testnet networks as present in `chains.yaml`), with full gRPC (emerald)
API support, so these chains can be removed from dshackle.

## Scope

- **In scope:** bitcoind-style JSON-RPC upstreams (Bitcoin Core, Dogecoin
  Core) with HTTP basic auth; the 19-method surface currently served by
  dshackle (see method table); optional **esplora** (electrs) secondary
  endpoint backing `listunspent`; head tracking, chain/health validation,
  client labels, prune-aware lower bounds; gRPC ChainRef support (already in
  the emerald proto for BITCOIN/DOGECOIN).
- **Out of scope (YAGNI):** wallet methods beyond the dshackle surface; ZMQ
  or WebSocket-style head subscriptions (bitcoind has none we consume);
  Litecoin/other UTXO chains (nothing registered in Consul today); deployment automation
  ansible filter changes (deferred until the family passes live validation
  from a laptop-run nodecore instance — see Testing).

## Approach

Bitcoin is modelled as a **JSON-RPC blockchain family** following the
Algorand/Aptos template: a `bitcoin_specific` chain-specific package plus a
data-driven method spec. A runtime-only extension is not viable for a new
family (compile-time factory switch), consistent with prior family additions.

One family covers both bitcoin and dogecoin: Dogecoin Core is a Bitcoin Core
fork with an API-compatible RPC surface for every method in scope.

### Verified live API shapes (from our production nodes)

`getblockchaininfo` (Bitcoin Core 26.1, mainnet; JSON-RPC 1.0 envelope,
`error: null` on success):

```json
{"result":{"chain":"main","blocks":958407,"headers":958407,
 "bestblockhash":"00000000000000000000d7c68a3b5e0794da056f7996c668620eb2b53591a8cf",
 "difficulty":127170500429035.2,"time":1784290128,"mediantime":1784286851,
 "verificationprogress":1,"initialblockdownload":false,
 "size_on_disk":860307387461,"pruned":false,"warnings":""},"error":null,"id":1}
```

Dogecoin Core (mainnet) returns the same shape with `"chain":"main"` as well —
**the `chain` field cannot distinguish bitcoin from dogecoin**; only the
genesis hash can (see Chain validation). Dogecoin's payload has no `time`
top-level field on older cores; head tracking must not depend on it.

`getnetworkinfo`: `{"version":260100,"subversion":"/Satoshi:26.1.0/",
"connections":10,...}` — source for the client version label and peer count.

`getblockhash 0`: bitcoin mainnet `000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f`,
dogecoin mainnet `1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691`.

Esplora (electrs) `GET /address/{addr}/utxo`:

```json
[{"txid":"548030c7...","vout":0,
  "status":{"confirmed":true,"block_height":903979,"block_hash":"000...f2f",
            "block_time":1751639616},"value":927}]
```

`GET /blocks/tip/height` → plain-text height (used only by the live-test
harness, not by nodecore).

## Architecture

### Factory and type plumbing

- `upstream_factory.go`: new `case chains.Bitcoin` returning
  `bitcoin_specific.NewBitcoinChainSpecificObject(...)`.
- `chains.Bitcoin` already exists in `chains.go`; `IsValidBlockchainType`
  already accepts it. `chains.yaml` already registers bitcoin/dogecoin with
  `type: bitcoin` — no registry work.

### Head tracking (`bitcoin_specific`)

RPC polling head (no subscriptions), same lifecycle as Algorand's:

1. Poll `getbestblockhash` at the configured poll interval.
2. On hash change, fetch `getblockheader <hash>` (verbose) for
   `height`/`time` and publish the new head.

`getblockheader` verbose is available on both Bitcoin Core and Dogecoin Core
and is far cheaper than a verbose `getblock`.

### Chain validation

`BitcoinChainValidator` fetches `getblockhash 0` once and compares against
the expected genesis hash for the configured chain. Expected hashes live in
the validator keyed by `chains.Chain` (BITCOIN, BITCOIN_TESTNET, DOGECOIN,
DOGECOIN_TESTNET); mainnet values verified above, testnet values verified
against our nodes during implementation. `getblockchaininfo.chain`
(`main`/`test`) is checked as a secondary signal. This mirrors what the
`chain-id` check does for EVM — genesis hash is the only reliable
discriminator between bitcoin and dogecoin.

### Health validation

`BitcoinHealthValidator` uses `getblockchaininfo`:
- syncing: `initialblockdownload == true` or `headers - blocks` above the
  chain's lag threshold;
- peers (when `validate-peers`): `getconnectioncount > 0`.

### Labels and lower bounds

- `client_version` label from `getnetworkinfo.subversion`
  (e.g. `/Satoshi:26.1.0/` → `26.1.0`).
- Lower bounds: if `getblockchaininfo.pruned == true`, `pruneheight` becomes
  the BLOCK/TX bound; otherwise bound 1 (archive). Our nodes are unpruned;
  the pruned path still gets unit coverage.

### Method spec (`pkg/methods/specs/bitcoin.json`)

The 19 methods dshackle serves, with equivalent semantics:

| group | methods | routing/quorum |
|---|---|---|
| fresh | getblock, gettransaction, gettxout, getmemorypool, getrawmempool, getmempoolinfo, getblockheader | default |
| not-null retry | getblockhash, getrawtransaction, estimatesmartfee | retry on null result |
| head-verified | getbestblockhash, getblocknumber, getblockcount, getreceivedbyaddress, getblockchaininfo | route to best-head upstream |

`getblocknumber` is not a real bitcoind method — it is a dshackle-era alias
kept for client compatibility; nodecore translates it to `getblockcount`.
`getmemorypool` is likewise ancient (dropped by modern Bitcoin Core) and is
kept passthrough-only for surface parity — the node's own error is returned.
| passthrough | getconnectioncount, getnetworkinfo | default (no dshackle-style hardcoding — these are our own nodes) |
| broadcast | sendrawtransaction | broadcast to all upstreams |
| esplora-backed | listunspent | translated, see below |

### Esplora translation (`listunspent`)

Esplora is an optional `rest-additional` connector on the upstream (the same
mechanism tron uses for its solidity endpoint). `listunspent` is intercepted
in `bitcoin_specific`: the address argument maps to
`GET /address/{addr}/utxo`, and the esplora response is converted to the
bitcoind `listunspent` result shape (txid, vout, address, amount in BTC from
sats, confirmations computed from current head vs `status.block_height`).
Upstreams without an esplora connector get `listunspent` banned via the
existing method-ban mechanism, so routing skips them (dogecoin has no
esplora today).

### Authentication

bitcoind requires HTTP basic auth. Connector URLs support userinfo
(`http://user:pass@host:port`) or an explicit `Authorization` header via the
connector `headers` field — whichever the existing connector plumbing already
handles; verified during implementation with a live node.

## Testing

1. **Unit tests** (template parity with aptos/algorand): head parsing,
   genesis validation incl. bitcoin-vs-dogecoin cross-check, health/syncing
   states, subversion label parsing, prune bounds, esplora→listunspent
   mapping on captured fixtures, method-spec loading.
2. **Live comparison harness (laptop, before any infra change).** nodecore
   runs locally on the laptop with a hand-written config; upstreams are our
   production nodes reached through ssh port-forwards (node RPC/esplora ports
   are firewalled to internal hosts). A corpus runner exercises **every**
   method in the spec against (a) the node directly and (b) nodecore
   (`/queries/bitcoin`), asserting identical results modulo volatile fields
   (connection counts, mempool contents, relative confirmations). Corpus
   includes: fresh blocks by height and hash, deep historical blocks, raw
   transactions, txout lookups, an address with known UTXOs (esplora path),
   estimatesmartfee, invalid-parameter negative cases, and unknown-method
   handling. `sendrawtransaction` is exercised for real only on
   **dogecoin-testnet**; on mainnets only with a deterministically invalid
   transaction (expected error passthrough), nothing is broadcast.
3. **Staged rollout (after laptop validation, separate step).** Wire the
   deployment automation (basic auth + esplora meta), enable bitcoin on
   one production nodecore instance while dshackle still serves the chain,
   compare error rates and responses, then drop `bitcoin`/`dogecoin` from
   `INCLUDE_TO_DSHACKLE`.

## Out of scope / follow-ups

- Deployment automation changes ship only after step 2 passes.
- near, ripple, starknet, ton follow as separate family designs reusing this
  template; celestia/stellar additionally need `chains.yaml` registry entries
  (they are absent there today).
