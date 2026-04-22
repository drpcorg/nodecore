# Quorum signature verification

nodecore can ask upstream providers to return signed responses and verify those signatures before returning data to the client. This lets a client be sure a response is the one the provider actually produced, with no tampering in between.

Quorum is currently supported only against drpc upstreams.

## Enabling quorum on a request

Quorum is opt-in per request via URL query parameters:

- `quorum=N` - ask the upstream for up to `N` independently-signed responses for this request.
- `quorum_required=M` - require at least `M` successfully verified signatures before the response is returned to the client.

Example:

```bash
curl --location 'http://localhost:9090/queries/ethereum?quorum=3&quorum_required=2' \
  --header 'Content-Type: application/json' \
  --data '{
    "id": 1,
    "jsonrpc": "2.0",
    "method": "eth_call",
    "params": [
      {"to":"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48","data":"0x313ce567"},
      "latest"
    ]
  }'
```

On success nodecore returns the usual JSON-RPC payload. If verification fails or not enough signatures are collected, the client gets a JSON-RPC error with code `-32010` describing the failure (missing signatures, insufficient signatures, invalid signature, unknown provider, etc.).

## What nodecore does

1. The upstream is selected from the configured drpc HTTP upstreams for the chain.
2. The `quorum` / `quorum_required` values are forwarded to the upstream.
3. The upstream returns the JSON-RPC response and one or more `QR*` headers, each carrying a provider identity, nonce and ECDSA signature.
4. nodecore parses every `QR*` header, looks the provider up in the embedded registry of known public keys, and verifies every signature against the exact response bytes.
5. Only if at least `quorum_required` signatures verify does the client receive the response; otherwise the request fails with a quorum error.

## Behavior caveats

Using quorum changes the request pipeline in a few ways. These are intentional - they exist so quorum actually does what it says.

- **Cache is bypassed.** Quorum requests never read from or write to the nodecore cache. A cached body would not carry the original `QR*` headers and verifying it against a fresh signature is not meaningful.
- **Integrity checks are bypassed.** The integrity re-verification layer is skipped for quorum requests - the signature check is the stronger guarantee for this request.
- **Responses are always unary (buffered).** Streaming responses cannot be signature-verified as a whole, so streaming is disabled for quorum requests even for methods that would otherwise stream.
- **Only drpc HTTP upstreams are eligible.** If the selected chain has no drpc upstream with an HTTP connector, the request fails with a `quorum not supported` error rather than falling back to an unsigned path.
- **Sticky-send methods are not supported.** Methods like `eth_sendRawTransaction` that must go to a single specific upstream are rejected with a `quorum not supported` error when quorum is requested.
- **HTTP only.** Quorum is available over HTTP(S) JSON-RPC. gRPC and WebSocket requests do not carry quorum params.

## Metrics

Per-request verification outcomes are exposed via Prometheus:

- `nodecore_quorum_verifications_total{chain, method, status, reason}` - incremented once per quorum request. `status` is `ok` or `fail`; on failure `reason` labels the cause (`missing_signatures`, `insufficient_signatures`, `invalid_signature`, `unknown_provider`, `malformed_header`, `request_id_mismatch`, `not_supported`, ...).
