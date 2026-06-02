# gRPC API

In addition to the HTTP JSON-RPC entrypoint described in the other docs, nodecore exposes a **general-purpose gRPC API** that external clients can use to query upstream / chain state and to execute RPC calls through the same upstream-selection, scoring, retry, and hedging machinery as the HTTP path. The gRPC surface is compatible with the [emerald-grpc / dshackle](https://github.com/drpcorg/emerald-grpc) protocol, so existing dshackle clients work against nodecore.

This API is intended for external consumption - dashboards, automation, multi-chain wallets, or any service that wants a typed, streaming view of the nodes that nodecore is fronting. It is not a control plane.

## Enabling

The gRPC server is off unless `server.grpc-port` is set. Optionally enable signed-session authentication via `server.grpc-auth`:

```yaml
server:
  port: 9090
  grpc-port: 9091
  tls:
    enabled: true
    certificate: /path/to/cert.pem
    key: /path/to/key.pem
  grpc-auth:
    enabled: true
    public-key-owner: drpc
    provider-private-key-path: /path/to/provider.key
    external-public-key-path: /path/to/external.pub
    session-ttl: 24h
```

When `server.tls.enabled: true`, the gRPC server shares the same certificate and listens with TLS. The gRPC port is independent of the HTTP port, the Prometheus port, and the pprof port - all four must be distinct (or zero / unset).

For the field semantics, see [Server config](02-server-config.md).

## Services and RPCs

The protocol definitions live in the [`emerald-grpc/`](../../emerald-grpc) submodule and are generated into [`pkg/dshackle/`](../../pkg/dshackle). Run `make dshackle-proto-gen` to regenerate the Go stubs after pulling submodule updates.

### `BlockchainService`

The main service. Exposes the following RPCs:

- **`SubscribeChainStatus(SubscribeChainStatusRequest) → stream ChainStatusEvent`**

  Server-streaming RPC. Emits an event whenever the state of any tracked chain changes (head moves, finality moves, an upstream becomes available / unavailable / syncing, labels change). On stream open, nodecore replays the current state for every chain it knows about so a fresh subscriber receives a complete snapshot before live updates begin.

  Use this to drive a real-time view of which upstreams are healthy, what block heights they are at, and which capability labels they carry.

- **`NativeCall(NativeCallRequest) → stream NativeCallReplyItem`**

  Server-streaming RPC. Executes one or more JSON-RPC calls against a configured chain. The call goes through nodecore's full execution flow - rating-based upstream selection, cache check, retries, hedging, integrity checks - just as if it had arrived over HTTP. Multiple items in one request are returned as separate stream items so a client can read partial results as they complete.

- **`NativeSubscribe(NativeSubscribeRequest) → stream NativeSubscribeReplyItem`**

  Server-streaming RPC. Opens a subscription on the target chain (e.g. `newHeads`, `logs` on EVM; `slotSubscribe`, `accountSubscribe` on Solana). The subscription is fulfilled through nodecore's shared-subscription registry, so multiple gRPC clients asking for the same subscription are coalesced onto a single upstream WebSocket. Periodic `Heartbeat: true` items are emitted (default every 30 seconds) so idle subscriptions don't look dead.

### `AuthService`

Used only when `server.grpc-auth.enabled: true`. The handshake is a single RPC that exchanges a signed JWT for a session ID:

1. The client constructs a JWT containing a `version: V1` claim and an `iat` (issued-at) timestamp, signs it with the *external* private key (whose public counterpart lives at `external-public-key-path` on the server side), and presents it.
2. nodecore verifies the signature with `external-public-key-path` and, on success, returns a session ID signed in turn with `provider-private-key-path`.
3. The client includes the session ID in the `sessionid` gRPC metadata header on every subsequent `BlockchainService` call.
4. Sessions auto-extend on every successful use, up to `session-ttl` of inactivity. Stale sessions are evicted.

This is the same handshake shape that the DRPC platform uses against its provider fleet. When `grpc-auth.enabled: false`, no session is required and all `BlockchainService` RPCs are open.

## Minimal client snippet (Go)

```go
package main

import (
    "context"
    "log"

    "github.com/drpcorg/nodecore/pkg/dshackle"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

func main() {
    conn, err := grpc.Dial("localhost:9091", grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    client := dshackle.NewBlockchainClient(conn)
    stream, err := client.SubscribeChainStatus(context.Background(), &dshackle.SubscribeChainStatusRequest{})
    if err != nil {
        log.Fatal(err)
    }

    for {
        event, err := stream.Recv()
        if err != nil {
            log.Fatal(err)
        }
        log.Printf("chain status: %+v", event)
    }
}
```

For a TLS or authenticated setup, swap `insecure.NewCredentials()` for proper credentials and add the `sessionid` to the outgoing metadata after completing the `AuthService` handshake.

## Observability

The gRPC API does not have a dedicated set of Prometheus metrics today; calls made through `NativeCall` and `NativeSubscribe` flow through the same request pipeline as HTTP calls, so they show up under the existing `nodecore_request_*` and `nodecore_upstream_*` metrics (see [Prometheus metrics](08-prometheus-metrics.md)).
