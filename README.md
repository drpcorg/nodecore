# Nodecore

A fault-tolerant, API-agnostic RPC load balancer for blockchain APIs.

> **⚠️ Project status**: nodecore is still under active development. Expect frequent updates and improvements as the project evolves.

## What is nodecore

nodecore sits in front of your blockchain RPC providers and intelligently distributes requests across them, optimizing for performance metrics such as latency, throughput, and error rate. It continuously scores upstreams in real time and routes each request to the one best able to serve it — with caching, hedging, retries, rate-limiting, and quorum verification layered in.

nodecore is **API/protocol-agnostic**: it is not tied to a single RPC shape. It speaks JSON-RPC, WebSocket, and REST interfaces, so it can front any blockchain API rather than only EVM JSON-RPC.

### Supported chains, methods & interfaces

- **Interfaces** — `json-rpc` (over HTTP), `websocket`, and `rest` upstream connectors. The set available for a given chain is declared by that chain's [method spec](docs/nodecore/11-method-specs.md).
- **Chains** — every chain defined in [`chains.yaml`](https://github.com/drpcorg/public/blob/main/chains.yaml). Current chain families are EVM (Ethereum, Polygon, Optimism, Arbitrum, Base, BSC, and many others), Solana, Algorand, Aztec, Aptos, NEAR, Ripple (XRP Ledger), Starknet, TON (v2 HTTP API + v3 indexer), Stellar (stellar-rpc + Horizon), and the Ethereum/Gnosis Beacon Chain (consensus layer, REST-only).
- **Methods** — per-chain RPC behavior is data-driven via [method specs](docs/nodecore/11-method-specs.md), covering standard EVM/Solana methods, subscriptions, and EVM filter methods. Methods unsupported by an upstream are automatically banned for it to avoid wasted requests.

## Key features

- **Intelligent routing** — dynamically selects the most suitable upstream based on real-time performance metrics (latency, error rate, availability) for optimal speed, reliability, and fault-tolerance. See [Upstream config](docs/nodecore/05-upstream-config.md).
- **API/protocol-agnostic** — JSON-RPC, WebSocket, and REST interfaces across EVM, Solana, Algorand, Aztec, Aptos, NEAR, Ripple (XRP Ledger), Starknet, TON, Stellar, and the Ethereum/Gnosis Beacon Chain, all driven by data-defined [method specs](docs/nodecore/11-method-specs.md). EVM filter methods (`eth_newFilter`, `eth_getFilterLogs`, etc.) are routed only to the upstream where the filter was created.
- **Subscriptions** — WebSocket subscriptions are aggregated so many identical client subscriptions share one upstream stream, with optional local synthesis of EVM topics (`newHeads`, `logs`, pending transactions). See [Subscriptions](docs/nodecore/13-subscriptions.md).
- **Caching** — minimizes redundant traffic by caching frequent requests across in-memory/Redis/Postgres backends with configurable policies. See [Cache](docs/nodecore/04-cache.md).
- **Failsafe mechanisms** — request hedging (duplicate slow requests to multiple upstreams) and configurable automatic retries. See [Upstream config](docs/nodecore/05-upstream-config.md).
- **Rate limiting** — per-method or pattern-based throttling of upstream traffic via reusable budgets or inline rules. See [Rate limiting](docs/nodecore/06-rate-limiting.md).
- **Quorum** — request and verify independently-signed responses from upstreams before returning data to the client. See [Quorum](docs/nodecore/10-quorum.md).
- **Flexible authentication** — token-based and JWT authentication, plus scoped access keys with fine-grained restrictions (IP, method, and contract address whitelists). See [Auth](docs/nodecore/03-auth.md).
- **Observability** — Prometheus [metrics](docs/nodecore/08-prometheus-metrics.md) and a public [gRPC API](docs/nodecore/12-grpc-server.md) for querying upstream and chain state.
- **Streaming-first architecture** — responses can be streamed to minimize memory footprint and handle large payloads efficiently.

## Quick start

Run with Docker, mounting your config:

```bash
docker run -p 9090:9090 -v /path/to/config:/nodecore.yml drpcorg/nodecore
```

A minimal config only needs at least one upstream; all other settings fall back to defaults:

```yaml
upstream-config:
  upstreams:
    - id: my-super-upstream
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://path-to-eth-provider.com
    - id: my-super-upstream-2
      chain: polygon
      connectors:
        - type: json-rpc
          url: https://path-to-polygon-provider.com
```

Send your first request:

```bash
curl --location 'http://localhost:9090/queries/ethereum' \
--header 'Content-Type: application/json' \
--data '{
    "id": 1,
    "jsonrpc": "2.0",
    "method": "eth_getBlockByNumber",
    "params": [
        "latest",
        false
    ]
}'
```

## Build from source

1. Clone the repository **with submodules** (required — builds fail without them):

   ```bash
   git clone --recursive https://github.com/drpcorg/nodecore.git
   ```

   If you already cloned without `--recursive`, run `git submodule update --init --recursive`.

2. Build the binary (this runs `make generate-networks` automatically):

   ```bash
   make build
   ```

3. Run it. `make run` reads `./nodecore.yml` by default; override the path with `NODECORE_CONFIG_PATH`:

   ```bash
   make run
   # or
   NODECORE_CONFIG_PATH=/path/to/your/config make run
   ```

   **Note**: `make run` does not regenerate chain data — run `make generate-networks` first if you haven't built. `nodecore-local.yml` is a gitignored file for your personal config.

## Documentation

Full documentation lives in [`docs/nodecore`](docs/nodecore). The canonical configuration schema and entry point is [01-config.md](docs/nodecore/01-config.md).

| Guide | What it covers |
| --- | --- |
| [Config](docs/nodecore/01-config.md) | Configuration entry point and full schema |
| [Server](docs/nodecore/02-server-config.md) | HTTP, gRPC, metrics, profiling, and TLS settings |
| [Auth](docs/nodecore/03-auth.md) | Token/JWT authentication and per-key access limits |
| [Cache](docs/nodecore/04-cache.md) | Cache storages and caching policies |
| [Upstream](docs/nodecore/05-upstream-config.md) | Upstream providers, failsafe, scoring, and labels |
| [Rate limiting](docs/nodecore/06-rate-limiting.md) | Per-method/pattern throughput control |
| [App storages](docs/nodecore/07-app-storages.md) | Shared Redis/Postgres connections |
| [Tor setup](docs/nodecore/07-tor-setup.md) | Running nodecore behind Tor |
| [Metrics](docs/nodecore/08-prometheus-metrics.md) | Prometheus metrics catalog |
| [Integration](docs/nodecore/09-integration.md) | DRPC platform integration |
| [Quorum](docs/nodecore/10-quorum.md) | Signed-response quorum verification |
| [Method specs](docs/nodecore/11-method-specs.md) | Per-chain method definitions and how to extend them |
| [gRPC API](docs/nodecore/12-grpc-server.md) | Public gRPC API for upstream and chain state |
| [Subscriptions](docs/nodecore/13-subscriptions.md) | Subscription aggregation and local synthesis |

## Integrations

nodecore can be integrated with external platforms to provide additional functionality — for example, DRPC for centralized key management and analytics. See [Integration](docs/nodecore/09-integration.md).

## Deployment

The Helm chart and deployment instructions are in [`chart/nodecore`](./chart/nodecore). It is also published as an OCI artifact to the GitHub Container Registry (GHCR).

## Special Deployments

### Running nodecore as a Tor hidden service

nodecore can be deployed as a Tor hidden service (`.onion`) for anonymous, censorship-resistant access:

```bash
docker-compose -f docker-compose.tor.yml up -d
cat tor-data/hostname  # Get your .onion address
```

See the [Tor setup guide](docs/nodecore/07-tor-setup.md) for full setup, security considerations, and troubleshooting.

## License

Released under the [MIT License](LICENSE).
