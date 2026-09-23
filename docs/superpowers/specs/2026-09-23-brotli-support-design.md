# Brotli (`br`) content coding — design

- **Date:** 2026-09-23
- **Status:** design approved, not yet implemented
- **Branch:** `feat/brotli-compression`
- **Area:** `internal/compression`, `internal/server/http_server`, `internal/upstreams/connectors`

## 1. Problem

nodecore speaks two HTTP content codings, gzip and zstd, on both of its edges
(`docs/nodecore/15-compression.md`). Brotli (`br`, RFC 7932) is the third coding
in common use, and its absence shows up in three places:

- **Client responses.** A client that accepts `br` but not `zstd` is served
  gzip. That is every Safari (`gzip, deflate, br`) and a long tail of HTTP
  libraries, and for a browser dApp it is the only coding better than gzip on
  offer.
- **Client request bodies.** `Content-Encoding: br` is an unknown coding to the
  ingress, so the body is passed through untouched and the handler fails to
  parse brotli bytes as JSON.
- **Upstream responses.** nodecore offers nodes `zstd, gzip`. A node that serves
  brotli but not zstd is held to gzip.

Observed on three public Ethereum mainnet providers (2026-09-23,
`eth_getBlockByNumber` with full transactions):

| provider | offered `zstd, br, gzip` | offered `br, gzip` | offered `br` alone | declared brotli window |
| -------- | ------------------------ | ------------------ | ------------------ | ---------------------- |
| A        | zstd                     | gzip               | br                 | lgwin 22 (4 MiB)       |
| B        | zstd                     | br                 | br                 | lgwin 16 (64 KiB)      |
| C        | zstd                     | gzip               | none (plain)       | —                      |

Every one of them prefers zstd when it is offered, so adding `br` to the offer
changes nothing on these three; it matters for nodes that do not speak zstd.

## 2. Goal

Add brotli as a third coding on both edges, with the same shape zstd has:

- the ingress serves `br` to clients that negotiate it, and decodes `br`
  request bodies;
- the HTTP-family upstream connectors offer `br` and decode `br` responses, on
  both the buffered and the streaming path;
- gzip and zstd behave as before, and no configuration is added.

Non-goals:

- config knobs or a kill switch — compression has none today and gains none;
- a decode concurrency budget — the zstd path has none either (see §8);
- a window cap tighter than RFC 7932 (see §3);
- brotli compound dictionaries, `x-gzip`/`deflate`, size thresholds;
- gRPC (own compressor registry) and WebSocket (`permessage-deflate`);
- compressing upstream request bodies — they stay plain.

## 3. Key decisions

| decision                  | outcome                                                                                    |
| ------------------------- | ------------------------------------------------------------------------------------------ |
| content-coding token      | `br` (RFC 7932 / IANA); `brotli` is an unknown coding like any other                       |
| server preference on ties | zstd > br > gzip, on the ingress and in the upstream `Offer`                               |
| upstream offer            | `Accept-Encoding: zstd, br, gzip` (unless pinned in connector `headers`, as today)         |
| library                   | `github.com/molecule-man/go-brrr` v1.1.1 — pure Go, poolable `Reset` on reader and writer   |
| encoder quality           | 1 (fixed)                                                                                  |
| encoder window            | lgwin 18 = 256 KiB, the same window as the zstd encoder                                    |
| decoder window            | the full RFC 7932 range, lgwin 10–24 (≤ 16 MiB); the non-standard large-window form is refused |
| eager validation          | decode one byte in `WrapReader`, since brotli has no magic number to check                 |
| `Negotiate`               | rewritten as one loop over the preference list; semantics unchanged                        |
| structure                 | extend `internal/compression` in place; the edges change comments only                     |

### Why quality 1 with a 256 KiB window

nodecore fixes every codec at its cheapest useful setting: compression sits on
the critical path of every proxied request. Measured on real mainnet bodies
(Apple M-series, pooled encoders, µs → bytes out):

| body                        | gzip BestSpeed      | zstd SpeedFastest   | br q1, lgwin 18    | br q2, lgwin 18     |
| --------------------------- | ------------------- | ------------------- | ------------------ | ------------------- |
| 45 B `eth_blockNumber`      | 1.4 → 70            | 0.1 → 58            | 2.0 → 49           | 1.4 → 49            |
| 26 KB block, hashes only    | 102 → 12,921        | 104 → 12,324        | 99 → 13,690        | 119 → 12,714        |
| 497 KB `eth_getLogs`        | 625 → 50,330        | 589 → 40,730        | 348 → 44,356       | 731 → 43,862        |
| 668 KB block, full txs      | 1,359 → 112,505     | 1,224 → 101,030     | 719 → 105,523      | 1,708 → 105,245     |
| heap held per used encoder  | 0.78 MiB            | 1.66 MiB            | 1.78 MiB           | 1.22 MiB            |

Quality 1 costs about half of gzip's CPU on large bodies and comes out 6–12%
smaller there. On mid-size bodies it matches gzip's CPU and is ~6% larger; on
tiny ones it is ~0.6 µs dearer and smaller. Quality 2 is ~7% denser than
quality 1 on mid-size bodies for 1.2–2.4× its CPU, and under 1% denser on large
ones. Quality 0 is cheaper still but 9–19% larger than gzip on large bodies,
which defeats the point. lgwin 18 beat lgwin 16 on both ratio and time on the
large bodies (105,523 vs 109,118 bytes on the block) for ~1 MiB more per
encoder, and matches the 256 KiB zstd window whose rationale (RPC bodies repeat
within kilobytes) applies unchanged. lgwin 22 bought 1–2% more for another
1.3 MiB per encoder.

zstd stays ahead of br on a tie: it is denser than brotli at these levels on
everything but the tiniest bodies, and far faster for the client to decode. br
is ahead of gzip: at quality 1 it is both cheaper and denser on the large bodies
where compression matters most.

### Why the full RFC 7932 window range

zstd's decoder is capped at 8 MiB because RFC 9659 caps zstd over HTTP there.
The equivalent limit for brotli is RFC 7932's own: lgwin 24, a 16 MiB window.
Staying inside the RFC means no conformant stream is ever refused:

- the reference `brotli` CLI declares lgwin 24 whenever it compresses a pipe
  (it only shrinks the window when it knows the input size), so
  `printf … | brotli | curl --data-binary @-` would otherwise get a 400;
- a node answering lgwin 24 would otherwise be scored as failing every
  compressed response.

The cost is on adversarial input: a crafted stream can make the decoder grow its
ring buffer to the declared window and flush a ring buffer's worth of output,
~2× the window, so up to ~32 MiB per concurrent decode, against ~8 MiB for a
crafted zstd frame. It is bounded per request, and a legitimate stream only
grows the ring buffer as far as its output needs. The large-window extension
(windows up to 1 GiB, outside RFC 7932) is rejected by the decoder itself
("invalid window bits").

## 4. Architecture

```
            ingress                                        upstream
client ─────────────────────► nodecore ─────────────────────────────► node
  Content-Encoding: br           │        Accept-Encoding: zstd, br, gzip
  (Decompress → WrapReader)      │        (applyConfigHeaders ← Offer)
                                 │
client ◄───────────────────── nodecore ◄───────────────────────────── node
  Accept-Encoding: …, br         │        Content-Encoding: br
  (Compress → Negotiate,         │        (decodeResponseBody → WrapReader)
   AcquireWriter)                │
```

Both edges already reach codecs only through `compression.Negotiate`,
`compression.Offer`, `compression.AcquireWriter`/`ReleaseWriter` and
`compression.WrapReader`. Brotli is added behind those four, so the middleware
and the connector pick it up without logic changes.

## 5. `internal/compression`

### 5.1 `compression.go`

- `Brotli Scheme = "br"`.
- `Offer = "zstd, br, gzip"` — written in preference order.
- `Negotiate` keeps its signature and semantics and changes shape: instead of a
  `gzipQ/zstdQ/gzipNamed/zstdNamed` variable pair per coding, it walks a fixed
  preference list `[Zstd, Brotli, Gzip]` holding a q and a "named" flag per
  entry. The rules it encodes are unchanged:
  - a coding named twice is settled by its last mention;
  - `*` supplies a q only for codings the client did not name;
  - identity wins only when named with a q strictly above every coding's;
  - q = 0 is a refusal and never wins;
  - the highest q wins, and an exact tie goes to the earlier list entry.
- `parseCoding` is unchanged.

### 5.2 `writer.go`

```go
const (
	brotliQuality    = 1
	brotliWindowBits = 18 // 256 KiB, the zstd encoderWindow
)

var brotliWriterPool = sync.Pool{New: func() any {
	w, err := brrr.NewWriterOptions(io.Discard, brotliQuality,
		brrr.WriterOptions{LGWin: brotliWindowBits})
	if err != nil {
		return err
	}
	return w
}}
```

- `*brrr.Writer` satisfies `Writer` natively (`Write`, `Flush`, `Close`,
  `Reset(io.Writer)`). `AcquireWriter` gains `case Brotli`, `ReleaseWriter`
  gains `case *brrr.Writer`.
- `AcquireWriter` always `Reset`s a checked-out writer, which marks it reused;
  go-brrr then keeps the compressor across `Close` instead of releasing it, so
  the pool holds warm encoders exactly as it does for zstd.
- `Flush` writes non-final meta-blocks and byte-aligns the stream, so a flushed
  prefix decodes on its own and streamed responses stay streamed. (go-brrr
  v1.1.0 did not byte-align below quality 2; fixed in v1.1.1, which is the
  minimum version, and pinned by a test.)
- The constants carry the §3 measurements in their comments, the way
  `encoderWindow` does.

### 5.3 `reader.go`

`WrapReader` gains `case Brotli: return wrapBrotliReader(r)`, backed by

```go
var brotliReaderPool = sync.Pool{New: func() any { return brrr.NewReader(nil) }}
```

`wrapBrotliReader(r)`:

1. **Read one raw byte.** Zero bytes (`io.EOF`) is an empty payload and returns
   `io.NopCloser(r)`, as gzip and zstd do: the decoder alone reports an empty
   source as "truncated input" and could not tell it from a truncated stream.
   Any other read error is `invalid brotli body: cannot read the stream header:
   %w`.
2. **Take a pooled `*brrr.Reader`** and `Reset` it onto that byte followed by the
   rest of `r`.
3. **Decode one byte eagerly.** brotli has no magic number, so this is what
   zstd's magic check is to zstd: it parses the window bits (refusing the
   reserved pattern and the large-window form) and the first meta-block header.
   Plaintext JSON (object, array, BOM-prefixed), gzip bytes, zstd bytes,
   random bytes and zeroes all fail here as `invalid brotli body: %w` — checked
   against go-brrr v1.1.1 — and the reader goes back to the pool. This is best
   effort: a mislabelled body whose leading bits happen to parse fails on a
   later `Read`, which is the contract gzip already has past its header. Note
   gzip's magic `0x1f` is also a legal brotli first byte (lgwin 24); the decode,
   not the byte, is what tells them apart.
4. **A valid stream that decodes to nothing** (the one-byte empty stream) returns
   its reader to the pool at once and hands back an empty `io.NopCloser`.
5. **Otherwise** return a `pooledReader` whose `Reader` is the decoded byte
   followed by the decoder, and whose `release` is
   `Close()` (hands the ring buffer back to go-brrr and zeroes the decode state)
   → `Reset(nil)` (revives the reader and drops `r`) → `Put`.

The existing `pooledReader` bookkeeping keeps `release` from running under a
live `Read`, which is what lets the streaming paths close a body from another
goroutine. Nothing about it is coding-specific.

The eager read in step 3 happens before `WrapReader` returns, so no `Close` can
race it. It blocks until the first meta-block produces output — the brotli
analogue of zstd blocking on its four magic bytes.

A pooled reader idles at ~43 KiB (its fixed 32 KiB input buffer). The ring
buffer lives in go-brrr's own process-wide pool between decodes and is cleared
by the GC.

## 6. The edges

### 6.1 Ingress (`internal/server/http_server`)

- **`compress.go`** — no logic change. `Negotiate` now returns `Brotli` where the
  client prefers it; the middleware labels the response `Content-Encoding: br`.
  Holding the status line until the first byte, bodyless responses and upgrades
  taking no encoder, flushing through the codec, `Vary: Accept-Encoding` on
  every response, and serving plain bytes when the pool fails all carry over.
  Comments that name "zstd or gzip" are updated.
- **`decompress.go`** — no logic change. `br` bodies are now decoded instead of
  passed through, `Content-Encoding` is removed after decoding, and the 32 MiB
  `MaxDecodedRequestBytes` cap applies to them.

### 6.2 Upstream (`internal/upstreams/connectors/http_connector.go`)

- `applyConfigHeaders` sends `compression.Offer`, now `zstd, br, gzip`, unless the
  connector's `headers` pin `Accept-Encoding`.
- `decodeResponseBody` goes through `WrapReader`, so `br` is decoded before the
  routing, quorum, caching and streaming machinery reads the body.
- Comments that say "both codings" are updated.

## 7. Error handling

All mappings exist today; brotli only feeds into them.

| where             | situation                                                   | result                                                         |
| ----------------- | ----------------------------------------------------------- | -------------------------------------------------------------- |
| client request    | `br` body that is not brotli (fails the eager decode)       | `400 invalid compressed request body`                          |
| client request    | empty body, or the one-byte empty stream, labelled `br`     | accepted; the handler reads an empty body                      |
| client request    | decodes past 32 MiB                                         | the read fails and the request is rejected, never truncated    |
| client request    | corruption deeper in the stream                             | fails when the handler reads it, as for gzip and zstd          |
| upstream response | not brotli, or truncated at the head                        | partial failure: retried elsewhere, the node is scored         |
| upstream response | corruption deeper in the stream                             | fails mid-read, as for gzip and zstd                           |
| upstream response | client leaves while the head is being decoded               | total failure with a context error; not counted against the node |
| either            | encoder pool cannot build a writer                          | served uncompressed, error logged                              |

## 8. Compatibility

- **Clients** that do not mention `br` see no change. A client sending
  `gzip, deflate, br` now gets br instead of gzip — cheaper to produce than gzip
  on large bodies (§3). Clients offering zstd as well (`…, br, zstd`) still get
  zstd.
- **Nodes** that speak zstd answer as before, which includes all three
  providers in §1. A node that speaks br and gzip but not zstd moves from gzip
  to br. An operator can restore the old offer per connector with
  `headers: {Accept-Encoding: "zstd, gzip"}`.
- **Memory.** A pooled brotli encoder holds ~1.8 MiB once used, about a zstd
  encoder's 1.7 MiB. A pooled decoder idles at ~43 KiB. A crafted `br` request
  body can make one decode peak at ~32 MiB (§3); nothing bounds the number of
  concurrent decodes, exactly as for zstd today.
- **Contract changes in existing tests.** `Negotiate` answers differently for
  three existing inputs, all because `br` or a wildcard now reaches a coding
  nodecore speaks:

  | input          | before   | after  |
  | -------------- | -------- | ------ |
  | `br, deflate`  | identity | br     |
  | `zstd;q=0, *`  | gzip     | br     |
  | `*, zstd;q=0`  | gzip     | br     |

  Tests that use `br` as their example of an unknown coding switch to `deflate`
  or `compress` (`compress_test.go`, `decompress_test.go`,
  `http_connector_compression_test.go`, `reader_test.go`), and the assertion
  that the offer is `zstd, gzip` becomes `zstd, br, gzip`.
- **Unchanged:** gRPC, WebSocket, upstream request bodies, all configuration.

## 9. Testing

Test-first, extending the existing files.

**`internal/compression`**

- `compression_test.go` — new `Negotiate` rows: `br` → br; `br, gzip` and
  `gzip, br` → br; `gzip, deflate, br` → br; `br, zstd` and `zstd, br` → zstd;
  `br;q=0.9, zstd;q=0.5` → br; `br;q=0.1, gzip` → gzip; `br;q=0, gzip` → gzip;
  `br;q=0, *` → zstd; `zstd;q=0, br;q=0, *` → gzip; `BR` → br;
  `br, br;q=0` → identity; `brotli` → identity; `identity;q=0.5, br` → br; plus
  the three changed rows in §8. `Offer` is pinned.
- `writer_test.go` — a round trip through `AcquireWriter`/`ReleaseWriter`; a
  flushed prefix decodes to everything written so far; a reused pooled writer
  produces independent, individually decodable streams; the stream header
  declares lgwin 18, pinning the encoder configuration.
- `reader_test.go` — decodes fixtures produced by the reference C `brotli` 1.2.0
  CLI and committed under `internal/compression/testdata` (lgwin 10 from a small
  file, lgwin 22, lgwin 24 from a pipe), an independent-implementation check that
  needs no second Go library; an empty body and the one-byte empty stream pass;
  a large-window stream (from the CLI's `--large_window`), plaintext JSON, a
  gzip body and a zstd body labelled `br` fail inside `WrapReader`; a stream
  truncated inside its head fails inside `WrapReader`, and one truncated past it
  fails on read; `BR` is accepted; the existing concurrent Close-during-Read test
  and the reuse-after-Close test cover `br`.

**`internal/server/http_server`**

- `compress_test.go` — br is served and decodes; the tie-break rows above hold
  end to end; a flushed br chunk reaches the client before the handler returns.
- `decompress_test.go` — a br body is decoded and its `Content-Encoding` removed;
  a malformed br body gets 400; an empty one is accepted; a br body decoding
  past 32 MiB is rejected.

**`internal/upstreams/connectors`**

- the offer sent is `zstd, br, gzip`; a br response decodes on the buffered and
  on the streaming path; a malformed br response is a partial failure; a pinned
  `Accept-Encoding: br` reaches the node verbatim and its answer is decoded.

**Gates:** `go test ./...`, `-race` on the three touched packages, `go vet`, the
repository linter.

## 10. Live verification

Before the PR, a local nodecore build with the three providers from §1 as
`json-rpc` upstreams, each behind a small logging pass-through proxy (kept in
the session scratchpad, never committed) that records the `Accept-Encoding`
nodecore sends and the `Content-Encoding` and first byte that come back:

1. **Default offer** — nodecore sends `zstd, br, gzip`; all three answer zstd.
2. **Pinned `Accept-Encoding: br`** — providers A and B answer br and nodecore
   decodes it; provider C answers plain and still works.
3. **Client matrix** — `br`, `gzip, deflate, br`, `br, zstd`, `zstd`, `gzip`, no
   header; each response compared with the plain one for a fixed block number,
   br responses decoded with the C `brotli -d`.
4. **br request bodies** — one from a pipe (lgwin 24) and one from a file; a
   non-brotli body labelled `br` gets 400.
5. **Load** — ~1,500 requests at 48-way concurrency across the coding
   combinations on both edges, every body validated, zero failures, nothing
   logged at error level.

Provider URLs and keys stay out of the repository and the PR.

## 11. Documentation

- `docs/nodecore/15-compression.md` — three codings throughout; the negotiation
  table (br rows; `br` leaves "not spoken"; the `zstd;q=0, *` row now reads br);
  request bodies; the upstream offer and the pinning example; a brotli column in
  the codec settings table (quality 1, window 256 KiB, decoder window up to
  16 MiB per RFC 7932); the brotli window under "Limits and safety"; brotli
  examples under "Verifying".
- `docs/nodecore/02-server-config.md` (`port`), `docs/nodecore/05-upstream-config.md`
  (connector `headers`), `README.md` (feature list and docs index) — one line
  each.

## 12. Key code references

- `internal/compression/compression.go` — `Scheme`, `Offer`, `Negotiate`
- `internal/compression/writer.go` — encoder pools, `AcquireWriter`, `ReleaseWriter`
- `internal/compression/reader.go` — decoder pools, `WrapReader`, `pooledReader`
- `internal/server/http_server/compress.go`, `decompress.go` — ingress middleware
- `internal/upstreams/connectors/http_connector.go` — `applyConfigHeaders`,
  `decodeResponseBody`, `decodedBody`
