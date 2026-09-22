package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The channel framing files a subscription under the client's own request id,
// which it reaches through RealIdHolder; only JSON-RPC requests carry one.
func TestJsonRpcRequestIsARealIdHolder(t *testing.T) {
	var holder protocol.RequestHolder = protocol.NewUpstreamJsonRpcRequest(
		"internal-uuid",
		protocol.JsonRpcRequestBody{Id: []byte(`"abc"`), Method: "header.Subscribe", Params: []byte(`[]`)},
		true,
		"celestia",
	)
	realId, ok := holder.(protocol.RealIdHolder)
	require.True(t, ok)
	assert.Equal(t, "abc", realId.RealId())

	var numeric protocol.RequestHolder = protocol.NewUpstreamJsonRpcRequest(
		"internal-uuid",
		protocol.JsonRpcRequestBody{Id: []byte(`5`), Method: "header.Subscribe", Params: []byte(`[]`)},
		true,
		"celestia",
	)
	assert.Equal(t, "5", numeric.(protocol.RealIdHolder).RealId())
}

func TestNonJsonRpcRequestsAreNotRealIdHolders(t *testing.T) {
	var rest protocol.RequestHolder = &protocol.UpstreamRestRequest{}
	var grpc protocol.RequestHolder = &protocol.UpstreamGrpcRequest{}
	_, restOk := rest.(protocol.RealIdHolder)
	_, grpcOk := grpc.(protocol.RealIdHolder)
	assert.False(t, restOk)
	assert.False(t, grpcOk)
}
