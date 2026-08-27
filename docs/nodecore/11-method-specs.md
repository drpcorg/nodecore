# Method specs

Method specs define the per-chain RPC method behavior that nodecore enforces at runtime: which methods exist on each chain, which transports they speak (`json-rpc` / `rest` / `grpc` / `websocket` / `tendermint`), whether each method is cacheable, whether it must stay on a single upstream ("sticky"), how to extract its block tag for cache-key derivation, and so on.

Specs are **data-driven**: nodecore does not hard-code any of this in Go. To add support for a new RPC method, add or edit a JSON spec file - don't add ad-hoc switches in code.

## Where specs live

Specs no longer live in this repository. They are maintained in
[`drpcorg/public`](https://github.com/drpcorg/public), a Go module shared by several drpc services (it also carries the chain registry and the emerald gRPC protocol), and nodecore consumes it as a regular dependency:

| Import path | Contents |
| --- | --- |
| `github.com/drpcorg/public/pkg/methods` | the spec JSON files (embedded), the loader (`NewMethodSpecLoader().Load()`) and the lookup helpers (`GetSpecMethod`, `IsSubscribeMethod`, ...) |
| `github.com/drpcorg/public/pkg/sui` | generated `sui.rpc.v2` protobuf types used by the Sui gRPC connector and the [gRPC chain ingress](14-grpc-ingress.md) |

The complete spec set shipped by the pinned module version is embedded into the nodecore binary; there is no need to copy or distribute the files separately.

The spec file format, the REST/tendermint routing conventions, and the list of shipped specs are documented in the module itself:
[`docs/method-specs.md`](https://github.com/drpcorg/public/blob/main/docs/method-specs.md).

## Changing specs

1. Open a PR against [`drpcorg/public`](https://github.com/drpcorg/public) and get it released (a semver tag).
2. Bump the dependency here: `go get github.com/drpcorg/public@<tag> && go mod tidy`.

To try a spec before it is released - or to add a spec that should stay local to one deployment - point `NODECORE_SPECS_PATH` at a directory of spec JSON files. They are loaded **in addition to** the embedded ones; a spec whose `name` already exists in the embedded set is rejected, so extras can only add new specs, not silently replace built-ins. See [Upstream config](05-upstream-config.md#extending-the-chain-registry-at-startup).
