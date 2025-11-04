# Running NodeCore as a Tor Hidden Service

This guide explains how to run NodeCore as a Tor hidden service (.onion), providing anonymity and privacy for your RPC endpoint.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Accessing Your Hidden Service](#accessing-your-hidden-service)
- [Security Considerations](#security-considerations)
- [Troubleshooting](#troubleshooting)
- [Advanced Configuration](#advanced-configuration)

## Overview

Running NodeCore inside Tor allows you to:

- **Anonymize your RPC endpoint**: Hide the real IP address of your NodeCore instance
- **Provide censorship resistance**: Make your service accessible even in restricted networks
- **Enable privacy-focused access**: Allow users to access blockchain data anonymously
- **Protect against DDoS**: Tor's routing makes targeted attacks more difficult

## Prerequisites

- Docker and Docker Compose installed
- Basic understanding of Tor and hidden services
- A configured `nodecore.yml` file (see [Configuration Guide](01-config.md))

## Quick Start

1. **Navigate to your NodeCore directory:**

```bash
cd /path/to/nodecore
```

2. **Ensure you have a valid configuration file:**

Make sure your `nodecore.yml` is properly configured with your upstream providers. You can use `nodecore-default.yml` as a template.

3. **Start the Tor hidden service and NodeCore:**

```bash
# Use default config (./nodecore.yml)
docker-compose -f docker-compose.tor.yml up -d

# Or specify a custom config via environment variable
NODECORE_CONFIG=/path/to/your-config.yml docker-compose -f docker-compose.tor.yml up -d

# Or use .env file
echo "NODECORE_CONFIG=./my-custom-config.yml" > .env
docker-compose -f docker-compose.tor.yml up -d
```

4. **Get your .onion address:**

Wait about 30-60 seconds for Tor to generate your hidden service keys, then run:

```bash
cat tor-data/hostname
```

This will output your .onion address (e.g., `abc123xyz456.onion`).

5. **Test your hidden service:**

Using a Tor-enabled client (like curl with torsocks):

```bash
torsocks curl --location 'http://your-onion-address.onion/queries/ethereum' \
--header 'Content-Type: application/json' \
--data '{
    "id": 1,
    "jsonrpc": "2.0",
    "method": "eth_blockNumber",
    "params": []
}'
```
