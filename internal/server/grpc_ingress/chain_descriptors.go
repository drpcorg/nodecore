package grpc_ingress

// Every gRPC service declared in the method specs must have its generated
// package linked here: the package's init() registers its descriptors into
// protoregistry.GlobalFiles, which is where the ingress reflection serves
// FileContainingSymbol from - tools like grpcurl and Postman encode real
// requests against these descriptors, so the set must stay complete.
//
// A vendored chain (pkg/sui) is one Go package covering all its services; a
// module-backed chain (e.g. cosmossdk.io/api) needs one line per service
// package its spec declares - the compiler pulls each one's message
// dependencies transitively. TestChainDescriptorsCoverSpecServices fails
// with the missing service name whenever a spec and this list diverge.
import (
	_ "github.com/drpcorg/public/pkg/cosmos"
	_ "github.com/drpcorg/public/pkg/sui"
)
