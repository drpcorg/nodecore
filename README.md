# Nodecore

----

## What is Nodecore

nodecore is a fault-tolerant RPC load balancer designed to work with any blockchain API. It intelligently distributes requests across multiple providers or nodes, with a focus on optimizing performance metrics such as latency, throughput, and error rates.

> **⚠️ Project status**: nodecore is still under active development. Expect frequent updates and improvements as the project evolves.

## Key features

* **Intelligent routing**: dynamically selects the most suitable upstream based on real-time performance metrics (latency, error rate, availability). This ensures optimal speed, reliability, and fault-tolerance
* **Multi-protocol RPC support**:
  * EVM and Solana JSON-RPC over HTTP and WebSocket
    * Supports all standard EVM and Solana RPC methods
    * Handles EVM filter methods (eth_newFilter, eth_getFilterLogs, etc.) by routing them only to the upstream where the filter was created
    * Supports all EVM and Solana subscription types
  * All methods and their settings are defined in the method [specs](pkg/methods/specs)
  * Unsupported methods are automatically banned per upstream to prevent unnecessary requests
  * Planned support for additional networks and protocols (REST, gRPC, etc.)
* **Efficient cache system**: Minimizes redundant traffic by caching frequent requests and reducing load on upstream providers
* **Customizable selection policies**: Offers flexible configuration for defining custom provider selection logic based on specific metrics and preferences
* **Streaming-first architecture**: Responses can be streamed to minimize memory footprint and handle large payloads efficiently
* **Failsafe mechanisms**: Built-in resilience features to ensure continuity under failure conditions, such as:
  * Request hedging – duplicate requests to multiple upstreams to mitigate slow responses
  * Automatic retries – configurable retries for failed requests
* **Flexible authentication**: Supports multiple authentication strategies, such as:
  * Token-based and JWT authentication for standard access control
  * Scoped access keys with fine-grained restrictions, such as IP whitelists, method whitelists, and contract address whitelists — allowing tighter limits per application

## Quick start

1. **Clone the repository:**

```bash
git clone https://github.com/drpcorg/nodecore.git
```

2. **Install Go dependencies:**

```bash
make setup
```

3. Create your own configuration file. Use ./nodecore.yml as a template. Replace the example URLs with your own upstream providers.

4. Run with docker:
    * Build the image `docker build -t nodecore:latest .`
    * Run the container with your local config `docker run -v ${/path/to/config}/nodecore-local.yml:./nodecore-local.yml nodecore:latest`

5. Run locally:
   * Generate chain info based on the public repo https://github.com/drpcorg/public `make generate-networks`
   * Start the service `make run`. **Note**: it will use the basic config file `./nodecore.yml`. If you want to specify your own one, run the following command: `NODECORE_CONFIG_PATH=/path/to/your/config make run`

6. Send you first request:
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

# Documentation

For detailed documentation [see](docs/nodecore)