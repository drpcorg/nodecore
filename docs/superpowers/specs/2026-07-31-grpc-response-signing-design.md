# gRPC response signing (NativeCall / NativeSubscribe)

Date: 2026-07-31
Branch: `sign_native`

## Problem

dshackle signs the responses it returns over gRPC when the client asks for it by putting a
non-zero `nonce` on the request. `NativeCall` signs each reply item
(`NativeCall.kt:420-427`), `NativeSubscribe` signs each event
(`NativeSubscribe.kt:161-168`), and both attach a `NativeCallReplySignature`
(`signature`, `key_id`, `upstream_id`, `nonce`). The key is the same RSA private key used for
gRPC auth: `ResponseSignerFactory` returns a `DisabledSigner` when auth is off or
`auth.server.provider-private-key` is unset, and an `RsaSigner` otherwise.

nodecore serves the same gRPC API from `internal/server/emerald` but ignores `nonce` entirely.
`NativeCallItem.Nonce` and `NativeSubscribeRequest.Nonce` exist in the generated proto
(`pkg/dshackle/blockchain.pb.go:300`, `:787`) and are never read. A client that asks nodecore
for a signed response gets an unsigned one, silently.

This adds the signing side. nodecore already implements the *verify* side of the exact same
scheme in `internal/quorum`, for checking QR-header signatures produced by dshackle upstreams.

## Wire format

Unchanged from dshackle, and already implemented by `quorum.WrapMessage`
(`internal/quorum/quorum.go:347`):

```
DSHACKLESIG/<nonce>/<source>/<hex(sha256(message))>
```

signed with RSA PKCS#1 v1.5 over SHA-256 of that string. `nonce` is formatted as an unsigned
decimal — dshackle uses `java.lang.Long.toUnsignedString` precisely so that nonces ≥ 2^63
round-trip (`RsaSigner.kt`, `wrapMessage` doc comment). Go's `uint64` gets this for free.

`source` is what dshackle passes as the signing source: the resolved upstream id.

**Key id** is `sha256(X.509 SubjectPublicKeyInfo DER)` of the public key, first 8 bytes read as
a big-endian 64-bit integer. dshackle does this with
`ByteBuffer.wrap(digest).asLongBuffer().get()`, which yields a signed Java `long`; the proto
field is `uint64`. Same 64 bits either way, so a verifier written against either side matches.

## Decisions

Five questions had more than one defensible answer. Resolved as:

1. **Streamed responses are not signed.** dshackle punts here too — `QuorumRequestReader.kt:161`
   carries a literal `// TODO: do streaming signature` and nulls the signature when
   `response.hasStream()`. A client that sets both `nonce` and `chunk_size` therefore gets no
   signature. Rejected: hashing incrementally and attaching the signature to the final chunk
   (works, but diverges — the client would have to know to look there).
2. **Results with no upstream are signed with `"NoUpstream"` as the source.** Cache hits
   (`internal/upstreams/flow/cache_request_processor.go:51`), locally-served synthetic methods
   (`request_processor.go:86`) and local subscriptions (`sub_processor.go:138`) all carry the
   literal `UpstreamId == "NoUpstream"` (`flow/strategy.go:15`). It is signed as-is rather than
   skipped. This diverges from `NativeSubscribe`, which drops the signature when
   `getSource()` is null; the tradeoff is that every signable reply is signed, at the cost of a
   source string that names no real node.
3. **A nonce with no signing key fails the item.** dshackle's `DisabledSigner.sign` throws
   `RpcException(CODE_INTERNAL_ERROR, "Response signing requested via nonce but signing key is
   not configured")`. A client that asked for a signature never gets a silently unsigned result.
4. **Scope is gRPC only.** The HTTP edge does not start emitting `QR<N>-id-*` headers, and
   upstream-provided signatures are not forwarded. dshackle prefers
   `response.providedSignature` over signing itself (`RequestReaderFactory.kt:44`) so that a
   nested dshackle's signature survives; nodecore verifies QR headers but does not retain them
   through the flow layer, so it always signs with its own key. Both are possible follow-ups.
5. **An explicitly configured `secure-signed` label wins over the injected one.** dshackle only
   injects when the key is absent (`!labels.containsKey(SECURE_SIGNED_LABEL)`), which lets an
   operator opt a single upstream out. Rejected: always overwriting with the signer's truth
   (config then cannot lie, but silently discards what the operator wrote), and failing
   validation on a contradiction (no silent lie, but a new rule dshackle does not have that
   rejects configs dshackle accepts).

## Design

### 1. New package `internal/signature`

```go
type Signature struct {
    Value      []byte
    UpstreamID string
    KeyID      uint64
}

type ResponseSigner interface {
    Enabled() bool
    Sign(nonce uint64, message []byte, source string) (Signature, error)
}
```

Two implementations:

- `rsaSigner` — holds an `*rsa.PrivateKey` and a `keyID` computed once at construction. `Sign`
  calls `quorum.WrapMessage(nonce, source, message)`, takes SHA-256 of the result, and signs
  with `rsa.SignPKCS1v15`. Reusing `quorum.WrapMessage` rather than restating the format is the
  point: nodecore's sign and verify sides cannot drift.
- `disabledSigner` — `Enabled()` is false and `Sign` returns an error carrying dshackle's
  message verbatim.

`internal/quorum` does not import `internal/signature`, so the dependency is acyclic.

### 2. Wiring

Entirely inside `NewGrpcServer` (`internal/server/emerald/grpc_server.go:27`) — no change to
`app.go` or `ApplicationServerContext`. A `newResponseSigner(cfg *config.GrpcAuthConfig)` helper
returns `disabledSigner` when `cfg.Disabled()` reports signing unavailable, and otherwise loads
that PEM and returns an `rsaSigner`. The signer is passed into `NewGrpcBlockchainService`.

`GrpcAuthConfig.Disabled()` (`internal/config/server_config.go`) is the single definition of
"signing is off":

```go
func (g *GrpcAuthConfig) Disabled() bool {
    return g == nil || !g.Enabled || g.ProviderPrivateKeyPath == ""
}
```

Section 6 needs the same rule at config-parse time, so it lives on the config type rather than
being restated at each use.

That re-reads the key file `NewGrpcAuthService` already reads (`grpc_auth.go:149`). One extra
read at startup, in exchange for the auth service and the signer staying independent —
threading a fourth return value out of `NewGrpcAuthService` is worse.

**No new config.** Signing is enabled exactly when grpc-auth is enabled with a provider key,
matching dshackle's condition.

### 3. Collapse the two reply modes

Prerequisite cleanup, in the code the signing change touches.

`nativeCallSuccessItems` (`native_call_adapter.go:205`, defined at `grpc_blockchain.go:247`)
currently takes `chunkSize` and, when a fully-buffered payload exceeds it, slices that in-memory
payload into several `Chunked: true` reply items. dshackle has no such path: `chunk_size` there
only sets `isStreamRequest` (`NativeCall.kt:355`) and is never used to split anything, so a
buffered response is always one item.

Remove the splitting:

- `nativeCallSuccessItems` becomes `nativeCallSuccessItem`, drops `chunkSize`, and returns a
  single `*dshackle.NativeCallReplyItem`.
- `SendReply` and `sendReply` drop their `chunkSize` parameter; `grpc_blockchain.go:112` drops
  the argument. `BuildRequest` keeps `chunkSize` — that is where it belongs, selecting a stream
  vs. non-stream request.
- `Chunked` / `FinalChunk` are then set only by `streamNativeCallBody`, i.e. only by the real
  streaming path.

There are then exactly two modes: streaming, selected by a non-zero `chunk_size` on the
request, and non-streaming. This is what makes the signing rule stateable in one line.

**Behavioral consequence beyond signing:** when a client sets `chunk_size` but the response
arrives buffered anyway — cache hit, locally-served method, a connector that did not stream —
it is now sent as a single item however large, where today it would be split. dshackle behaves
this way, so a client that talks to dshackle already tolerates it.

### 4. NativeCall

The per-item nonce has to survive from request construction to reply send.
`buildNativeCallRequests` (`grpc_blockchain.go:225`) returns
`adapters map[string]nativeCallAdapter` keyed by request id; the value becomes a small struct
carrying the adapter and the item's nonce, so the response loop does one lookup.
`SendReply` / `sendReply` take the nonce and the signer.

In `sendReply`:

| case | signature |
| --- | --- |
| error item | none — dshackle only signs `CallResult.ok` |
| `HasStream()` | none, **and the disabled-signer check does not fire** |
| buffered success, `nonce == 0` | none |
| buffered success, `nonce != 0` | sign `wrapper.Response.ResponseResult()`, source `wrapper.UpstreamId` |

The signature goes on the reply item next to the other response-level metadata
(`UpstreamId`, `Finalization`, `ResponseHeaders`).

The disabled-signer check firing only where a signature would actually have been produced is
deliberate: dshackle never calls `getSignature` on the stream path, so a streamed reply goes out
unsigned rather than failing, even with a `DisabledSigner`. Failing early at the top of
`NativeCall` for any nonce'd item would be friendlier but would diverge for streams.

A signing failure — disabled signer, or an RSA error — replaces the item with
`nativeCallErrorItem(requestID, protocol.ServerErrorWithCause(err), …)`, matching dshackle's
`CODE_INTERNAL_ERROR`.

### 5. NativeSubscribe

`nonce := request.GetNonce()` is read once before the event loop
(`grpc_blockchain.go:120`). For each event, when the nonce is non-zero,
`subscriptionResponse.ResponseResult()` is signed with source `wrapper.UpstreamId` and the
signature set on the `NativeSubscribeReplyItem`. Heartbeats are never signed.

A signing failure ends the stream with `codes.Internal` — dshackle parity, since its throw
inside `convertToProto` terminates the flux.

Per decision 2, locally-synthesized subscription events are signed with source `"NoUpstream"`
rather than left unsigned as dshackle leaves them.

### 6. The `secure-signed` upstream label

dshackle advertises its signing capability as a label on every upstream:
`UpstreamCreator.buildUpstreamLabels` injects `secure-signed=true` when `signer.enabled`.
Nothing inside dshackle reads it — it exists so a client can select signing-capable providers.

nodecore gets the same label at **config-parse time**, which is possible because manual upstream
labels landed in `d03dcae`. `Upstream.Labels` (`internal/config/upstream_config.go:133`) is
already seeded into the live upstream state at `internal/upstreams/upstream.go:86`, so a label
injected into config reaches the real label set — which matters, because a client's
`RequestLabelSelector` is compiled into `NewLabelMatcher` (`internal/upstreams/flow/selectors.go:67`)
and matched against exactly those labels. Injecting only at the gRPC mapping boundary
(`chain_event_mapper.go`) would advertise the label while leaving it unroutable, so a client that
echoed it back as a selector would match no upstream.

`AppConfig.setDefaults` already runs `ServerConfig.setDefaults()` (`defaults.go:32`) before
`UpstreamConfig.setDefaults()` (`:50`), so the grpc-auth config is populated in time.
`UpstreamConfig.setDefaults` gains a `grpcAuth *GrpcAuthConfig` parameter and, in its existing
upstream loop (`defaults.go:256`), calls a new `(*Upstream).setSecureSignedLabel()` whenever
`!grpcAuth.Disabled()`. `Upstreams` is `[]*Upstream`, so the mutation sticks.

`setSecureSignedLabel` lazily allocates `Labels` when nil and returns without touching an
existing `secure-signed` entry (decision 5). The constant is
`config.SecureSignedLabel = "secure-signed"`, matching dshackle's `SECURE_SIGNED_LABEL`.

Two consequences worth recording:

- `Disabled()` tests config shape, not whether the PEM actually parses. A corrupt key means the
  label is injected but `newResponseSigner` fails and `NewGrpcServer` returns an error, so the
  process never starts — the label cannot outlive a signer that failed to build.
- Config labels are seeds that a detector owning the same key overwrites on its first round
  (`upstream.go:83`). No detector owns `secure-signed`, so it survives.

## Testing

### `internal/signature`

- **Key id derivation.** Fixed test RSA key; assert the id equals
  `binary.BigEndian.Uint64(sha256(MarshalPKIXPublicKey(pub))[:8])` recomputed in the test, so
  the property is asserted rather than pinned to a magic constant.
- **Round trip against the verifier.** Sign with a test key, verify through a `quorum.Registry`
  built from the matching public key via `quorum.Verify(providerID, upstreamID, nonce, sig,
  result)`. This is the load-bearing test: `internal/quorum` was written against the dshackle
  spec independently of this signer, so a green round trip means wrap format, digest and padding
  all match dshackle.
- **Nonce ≥ 2^63** round trip, pinning the unsigned formatting that dshackle needed
  `toUnsignedString` for.
- **Tampering** — flipped result byte, wrong source, wrong nonce — fails verification.
- `disabledSigner` reports `Enabled() == false` and returns the error from `Sign`.

### `internal/server/emerald`

`sendReply` is already directly testable (`TestNativeCallSendReply*`,
`grpc_blockchain_test.go:401`) and the stream fakes exist (`testNativeCallStream:503`,
`testNativeSubscribeStream:535`).

- `nonce == 0` → no signature on the reply item.
- `nonce != 0`, buffered success → signature present with the expected `nonce`, `upstream_id`
  and `key_id`, verifying against the public key over `ResponseResult()`.
- `HasStream()` + nonce → no signature, no error, including with a disabled signer.
- Error response + nonce → no signature.
- Cache-hit wrapper (`UpstreamId == "NoUpstream"`) + nonce → signed with `"NoUpstream"`.
- Disabled signer + nonce, buffered path → error item carrying dshackle's message, not an
  unsigned success.
- Batch `NativeCall` with a nonce on one item and not the other → only the first is signed.
- `NativeSubscribe` → each event signed, heartbeats unsigned; a signing failure terminates the
  stream with `codes.Internal`.
- Buffered response larger than `chunk_size` → a single unchunked item (guards the section 3
  cleanup).

**Mechanical fallout:** `TestNativeCallSuccessItemsChunking` (`grpc_blockchain_test.go:381`) is
deleted, the three `SendReply(stream, wrapper, 0)` call sites lose their `chunkSize` argument,
and the nine `NewGrpcBlockchainService(nil, nil)` call sites gain a third parameter.

### `internal/config`

- `Disabled()` truth table: nil receiver, `enabled: false`, enabled with an empty
  `provider-private-key-path`, enabled with a path.
- `setSecureSignedLabel` on an upstream with no labels at all (allocates the map), with
  unrelated labels (adds alongside), and with `secure-signed` already set to something else
  (leaves it untouched — decision 5).
- `AppConfig.setDefaults` end to end: every upstream carries `secure-signed=true` when grpc-auth
  is enabled with a key, and no upstream carries it when grpc-auth is absent or keyless.

## Documentation

The `labels` field entry in `docs/nodecore/05-upstream-config.md:706` gains a note that
`secure-signed=true` is injected on every upstream when grpc-auth response signing is
configured, and that an explicitly configured `secure-signed` label takes precedence — the same
shape as the `archive: false` exception already documented there.

`docs/nodecore/12-grpc-server.md` covers the gRPC surface and should mention that a nonce on
`NativeCall`/`NativeSubscribe` returns a signed reply, and that signing-capable upstreams carry
`secure-signed=true`.
