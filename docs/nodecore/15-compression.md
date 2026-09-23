# Compression

nodecore speaks three HTTP content codings — **gzip**, **zstd** and **brotli** (`br`) — on both of
its edges, and the two edges are independent of each other:

```
                  ingress                                  upstream
   client  ──────────────────────►  nodecore  ──────────────────────►  node
           Content-Encoding: br              Accept-Encoding: zstd, br, gzip
           (request body, decoded)           (request body always plain)

   client  ◄──────────────────────  nodecore  ◄──────────────────────  node
           Accept-Encoding: gzip             Content-Encoding: zstd
           (re-encoded for the client)       (decoded on arrival)
```

A body is **decoded on arrival and re-encoded on departure**, never forwarded still-compressed. That
is what makes the hops independent: a client asking for gzip is served gzip even when the upstream
answered in zstd, and a client sending brotli reaches a node that has never heard of it.

There is **no configuration** for any of this. Content negotiation settles it per request, and a peer
that knows none of the codings simply gets plain bytes. The one knob that exists is a per-connector
override for [pinning what nodecore asks upstreams for](#pinning-the-upstream-coding).

## Ingress

Applies to the main HTTP port (`server.port`, see [Server config](02-server-config.md)) — every
route under `/queries/:chain`, for JSON-RPC and REST alike. The metrics, pprof, and health ports are
not compressed.

### Responses to clients

The coding is picked from the client's `Accept-Encoding` per RFC 9110 §12.5.3. The highest q wins,
and an exact tie goes to **zstd, then br, then gzip**: zstd is the densest of the three and the
fastest for a client to decode, and br beats gzip on both size and CPU on the bodies large enough
for either to matter.

| `Accept-Encoding`        | Coding served | Why                                                           |
| ------------------------ | ------------- | ------------------------------------------------------------- |
| _(absent)_               | none          | Nothing was asked for                                         |
| `gzip`                   | `gzip`        |                                                               |
| `zstd`                   | `zstd`        |                                                               |
| `br`                     | `br`          |                                                               |
| `gzip, zstd`             | `zstd`        | Equal q — zstd wins the tie                                   |
| `gzip, deflate, br`      | `br`          | Equal q — br outranks gzip on a tie                           |
| `br, zstd`               | `zstd`        | Equal q — zstd outranks br on a tie                           |
| `*`                      | `zstd`        | The wildcard reaches every coding at the same q               |
| `gzip;q=1.0, zstd;q=0.5` | `gzip`        | Highest q wins                                                |
| `br;q=0.9, zstd;q=0.5`   | `br`          | Highest q wins                                                |
| `zstd;q=0, *`            | `br`          | `*` stands in only for codings the client did not name itself |
| `gzip;q=0`               | none          | `q=0` is a refusal, not a weak preference                     |
| `identity`               | none          | Plain bytes ranked above every offered coding                 |
| `identity;q=0.5, gzip`   | `gzip`        | gzip outranks identity                                        |
| `brotli`, `deflate`, …   | none          | Not spoken here — brotli's token is `br`                      |

Details that matter in practice:

- **Several header lines are joined**, not just the first one read. `Accept-Encoding: zstd;q=0`
  followed by a second `Accept-Encoding: gzip` line means what one comma-separated line would.
- **A coding named twice is settled by its last mention**, so a merged `gzip, gzip;q=0` expresses a
  refusal rather than contradicting itself.
- **Nothing acceptable yields plain bytes, not `406`.** `identity;q=0` alone, or `*;q=0`, gets an
  uncompressed body — the one response an RPC client can actually use.
- **`Vary: Accept-Encoding` is always sent**, including on uncompressed responses, so a shared cache
  cannot hand a zstd body to a gzip-only client. When CORS is active, `Vary: Origin` is _added_
  alongside it rather than replacing it.
- **No size threshold.** Every response with a body is encoded; there is no minimum length below
  which compression is skipped.
- **Streamed responses stay streamed.** A flush pushes bytes through the codec and on to the socket,
  so chunks reach the client without waiting for the response to finish.
- **Bodyless responses carry no `Content-Encoding`** and cost no encoder — a `204`, a `304`, or a
  WebSocket upgrade that hijacks the connection.
- `Content-Length` is dropped from a compressed response (the encoded length is not known up front).
- If the codec pool cannot hand out an encoder, the response is served **uncompressed** rather than
  failed, and the error is logged.

### Request bodies from clients

A request body arriving with `Content-Encoding: gzip`, `zstd` or `br` is decoded before any handler
sees it, so handlers always read plain bytes.

- The `Content-Encoding` header is **removed** after decoding — left in place it would be forwarded
  to an upstream and tell a node to decompress a body that is already plain.
- A **coding nodecore does not speak** (`deflate`, `compress`, …) is passed through untouched rather
  than guessed at; the handler sees the bytes as sent.
- A body that claims a coding but is **not valid** under it is rejected with `400 Bad Request`
  (`invalid compressed request body`). Every coding is checked before the handler runs — gzip parses
  its header, zstd checks its frame magic, and brotli, which has no magic number, decodes its first
  byte — so this fails at the edge rather than halfway through parsing.
- A brotli body with **bytes after the end of its stream** — junk, or a second stream — fails, as
  gzip and zstd fail on bytes after their last member or frame.
- An **empty body** is accepted whatever coding it claims — a peer that labels an empty `204` with a
  `Content-Encoding` is not an error worth failing.
- A decoded body is capped at **32 MiB** (`MaxDecodedRequestBytes`). Past the cap the read fails
  rather than returning a short body, so a truncated request is rejected instead of silently parsed.
  See [Limits](#limits-and-safety) for why.

## Upstream

Applies to the HTTP-family connectors — `json-rpc`, `tendermint`, `rest`, `rest-indexer`,
`rest-additional`. See [Upstream config](05-upstream-config.md#connectors).

**Requests to upstreams are never compressed.** nodecore sends plain request bodies to nodes;
compression on this hop is response-only.

**Responses from upstreams are requested compressed.** Every outgoing request carries:

```
Accept-Encoding: zstd, br, gzip
```

and whatever comes back — `zstd`, `br`, `gzip`, or plain — is decoded before the routing, quorum,
caching, and streaming machinery downstream ever reads it. A node that speaks none of the codings
answers plain and nothing changes.

A client's own `Accept-Encoding` is **never forwarded** to an upstream. It belongs to the client hop
alone: the two hops compress independently, so a client's preference says nothing about what this
connector should ask a node for. (Forwarding it is also how a body once got compressed twice —
issue #268.)

If a response body cannot be decoded — a node that labels a body with a coding it did not actually
use — the request is a **partial failure** against that upstream: it is retried elsewhere and the
node is scored for it. A client that walked away mid-decode is not counted against the node.

### Pinning the upstream coding

A node that mishandles a coding can be pinned with the connector's `headers` map, which is applied
before the default offer and therefore wins:

```yaml
upstreams:
  - id: some-node
    chain: ethereum
    connectors:
      - type: json-rpc
        url: https://node.example.com
        headers:
          Accept-Encoding: zstd, gzip # never ask this node for brotli
      - type: rest
        url: https://rest.example.com
        headers:
          Accept-Encoding: identity # no compression from this node at all
```

Header names are matched case-insensitively. This affects only that connector; the client-facing
side is unchanged.

## Codec settings

Levels are fixed. On a proxy the compression sits on the critical path of every request, where CPU
time is worth more than the last few percent of ratio.

|                    | gzip            | zstd           | brotli                                |
| ------------------ | --------------- | -------------- | ------------------------------------- |
| Encoder level      | `BestSpeed`     | `SpeedFastest` | quality 1                             |
| Encoder window     | library default | 256 KiB        | 256 KiB (lgwin 18)                    |
| Decoder window cap | n/a             | 8 MiB          | 16 MiB (lgwin 24, RFC 7932's maximum) |
| Concurrency        | 1 worker        | 1 worker       | 1 worker                              |

Encoders and decoders are **pooled** and shared by both edges: a zstd codec allocates its window up
front, which is far too expensive to repeat per proxied request. The 256 KiB encoder window is sized
for what RPC bodies actually repeat — method names, hex prefixes, key names, all within a few
kilobytes — so the default multi-megabyte window would buy almost no ratio for memory multiplied by
every encoder in flight. Concurrency is pinned to one worker per codec because the default spawns
`GOMAXPROCS` goroutines per codec, which on a proxy holding thousands of concurrent responses is a
goroutine count nobody asked for.

Brotli runs at quality 1 rather than its fastest, 0: quality 0 comes out larger than gzip on large
bodies, while quality 1 costs about half of gzip `BestSpeed`'s CPU there and comes out smaller. A
pooled brotli decoder keeps only its fixed 32 KiB input buffer between bodies.

gzip and zstd are provided by [`klauspost/compress`](https://github.com/klauspost/compress), brotli
by [`molecule-man/go-brrr`](https://github.com/molecule-man/go-brrr).

## Limits and safety

Two independent caps bound what a compressed body can make nodecore commit. Both apply to whoever
sent the body — the client on the ingress, the node upstream.

**Decoder window: 8 MiB for zstd, 16 MiB for brotli.** A zstd decoder allocates the window its frame
header declares, up front, so a peer that names a large one turns a few hundred bytes on the wire
into hundreds of megabytes of heap (the library's own ceiling is 512 MiB). 8 MiB is the limit RFC
9659 §3 sets for zstd over HTTP: decoders must support up to 8 MB and encoders must not generate
frames requiring more. General-purpose zstd does produce larger frames — `zstd --long` alone defaults
to a 128 MiB window — so a frame above the cap is rejected rather than allocated for.

brotli's cap is RFC 7932's own limit, lgwin 24 (16 MiB). It is the window the reference `brotli` CLI
declares whenever it compresses a pipe, so no conformant stream is refused; the non-standard
large-window form (up to 1 GiB) is. A crafted brotli stream can make one decode allocate about twice
its declared window — up to ~32 MiB, against ~8 MiB for a crafted zstd frame — while a legitimate
stream only grows the decoder's buffer as far as its output needs.

**Decoded request body: 32 MiB.** The window cap bounds what a frame _header_ can ask for; this
bounds what the decoded _bytes_ can. A compressed body is a size multiplier whose factor the sender
chooses — DEFLATE tops out near 1000:1, and zstd and brotli have no comparable ceiling — so without
it a few hundred kilobytes on the wire can ask for gigabytes, from anyone who can reach the port.
32 MiB is far above anything JSON-RPC produces in practice (a batch of ten thousand calls is on the
order of a megabyte), so it is a ceiling on abuse rather than a limit real traffic meets. It applies
to client request bodies only; upstream responses are bounded by the connector's
[`response-timeout`](05-upstream-config.md#connectors) instead.

## What is not covered

- **WebSocket frames.** The handshake hijacks the connection before any encoder is involved, and
  frame-level compression (`permessage-deflate`) is not negotiated. See
  [Subscriptions](13-subscriptions.md).
- **gRPC.** Both the [gRPC API](12-grpc-server.md) and the
  [gRPC chain ingress](14-grpc-ingress.md) use gRPC's own per-message compression, negotiated with
  `grpc-encoding` rather than `Accept-Encoding`, and entirely separate from this. Only **gzip** is
  registered as a gRPC compressor; zstd and brotli are not. The `grpc` upstream connector does not
  request compressed messages from nodes.

## Verifying

```bash
# zstd response
curl -sS -D- -o/dev/null -H 'Accept-Encoding: zstd' \
  -X POST http://localhost:9090/queries/ethereum \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'
# ... Content-Encoding: zstd
# ... Vary: Accept-Encoding

# gzip response, decoded by curl
curl -sS --compressed -H 'Accept-Encoding: gzip' \
  -X POST http://localhost:9090/queries/ethereum \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'

# brotli response, decoded by the reference CLI (not every curl build decodes br)
curl -sS -H 'Accept-Encoding: br' \
  -X POST http://localhost:9090/queries/ethereum \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' | brotli -dc

# gzip-compressed request body
printf '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' | gzip \
  | curl -sS -X POST http://localhost:9090/queries/ethereum \
      -H 'Content-Encoding: gzip' -H 'Content-Type: application/json' --data-binary @-

# brotli-compressed request body
printf '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' | brotli -c \
  | curl -sS -X POST http://localhost:9090/queries/ethereum \
      -H 'Content-Encoding: br' -H 'Content-Type: application/json' --data-binary @-
```

A response with no `Content-Encoding` where one was asked for means the client's header did not
negotiate a coding nodecore serves — check the [table above](#responses-to-clients), particularly
`q=0`, `identity`, and `brotli` spelled out instead of `br`.
