package emerald

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
)

// nativeSubscribeAdapter bridges one NativeSubscribe request to the flow and
// back, per transport: the JSON-RPC adapter serves eth_subscribe-style
// subscriptions and native JSON-RPC sub methods, the gRPC adapter serves
// server-streaming gRPC methods. The handler owns the pre-dispatch checks,
// the flow and the heartbeat loop; the adapter owns the wire shape.
type nativeSubscribeAdapter interface {
	// BuildRequest turns the subscribe request into a flow request. An error
	// is a pre-dispatch failure and is returned to the client as the
	// NativeSubscribe stream status, so it must already be a gRPC status error.
	BuildRequest(
		chain *chains.ConfiguredChain,
		chainSupervisor upstreams.ChainSupervisor,
		request *dshackle.NativeSubscribeRequest,
	) (protocol.RequestHolder, error)

	// SendReply renders one flow response onto the stream. done reports that
	// the subscription is over: the handler then returns err (nil for a clean
	// in-band end). A non-nil err always ends the handler with that error.
	SendReply(
		stream dshackle.Blockchain_NativeSubscribeServer,
		wrapper *protocol.ResponseHolderWrapper,
		nonce uint64,
		signer signature.ResponseSigner,
	) (done bool, err error)
}

// subscribeAdapterFor picks the transport from the spec, never from the
// request's data oneof: metadata is optional on a gRPC subscribe, and a
// JSON-RPC subscribe carries none. Unknown methods take the JSON-RPC adapter,
// whose mapping reports the precise error.
func subscribeAdapterFor(specName, method string) nativeSubscribeAdapter {
	specMethod := specs.GetSpecMethod(specName, method)
	if specMethod != nil && specMethod.GrpcCallType().IsServerStream() {
		return grpcNativeSubscribeAdapter{}
	}
	return jsonRpcNativeSubscribeAdapter{}
}
