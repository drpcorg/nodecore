# Mova chain support

## Goal

Serve Mova mainnet ("The Intelligent Value Layer") through nodecore.

## Chain facts

- Cosmos SDK / Tendermint consensus with an ethermint EVM module; the
  rest-server exposes standard EVM JSON-RPC on HTTP and WS.
- EVM chain id 61900 (`0xf1cc`), ~2s block time (measured against the public
  RPC at rpc.movachain.com).
- Exposed namespaces are `web3`, `eth`, `net` only - no `debug`, `trace` or
  `txpool`. nodecore's automatic per-upstream method banning covers the gap;
  no spec surgery needed.
- A testnet ("Mova Beta"/mars, chain id 10323) exists but upstream publishes
  no node configs for it, so it is out of scope.

## Design

Mova is a plain EVM family member: the whole integration is a chains.yaml
entry in drpcorg/public (type `eth`, `validate-peers: false` since
`net_peerCount` is not meaningful on a cosmos hybrid, lags scaled to the 2s
block time) plus the `pkg/chains/public` submodule bump here. The generated
chain registry gives it the `eth` method spec and the EVM chain-specific
object; no nodecore code changes.

## Testing

`make generate-networks` + the existing registry/config/method-spec test
suites; manual resolution check (`chains.GetChain("mova")` returns type eth,
chain id 0xf1cc, eth spec, 2s block time).
