package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
)

func TestNewUpstreamGrpcRequestStoresMethodAndBodyVerbatim(t *testing.T) {
	body := []byte{0x0a, 0x03, 0x66, 0x6f, 0x6f}
	req := protocol.NewUpstreamGrpcRequest("test-id", "/sui.rpc.v2.LedgerService/GetObject", nil, body, "")

	assert.Equal(t, "/sui.rpc.v2.LedgerService/GetObject", req.Method())
	assert.Equal(t, protocol.Grpc, req.RequestType())
	assert.Equal(t, "test-id", req.Id())
	gotBody, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, body, gotBody)
	assert.NotNil(t, req.RequestObserver(), "observer must be non-nil so ObserverConnector doesn't panic")
	assert.False(t, req.IsStream())
	assert.False(t, req.IsSubscribe())
	assert.Nil(t, req.ParseParams(t.Context()))
}

func TestUpstreamGrpcRequestCarriesMetadataAsHeaders(t *testing.T) {
	params := &protocol.RequestParams{
		Headers: map[string][]string{"x-custom-meta": {"a", "b"}},
	}
	req := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Method", params, nil, "")

	assert.Equal(t, params, req.RequestParams())
}

func TestUpstreamGrpcRequestHash(t *testing.T) {
	base := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Method", nil, []byte{1, 2}, "")

	same := protocol.NewUpstreamGrpcRequest("2", "/pkg.Service/Method", nil, []byte{1, 2}, "")
	assert.Equal(t, base.RequestHash(), same.RequestHash(), "id must not affect the hash")
	assert.Equal(t, base.RequestHash(), base.RequestHash(), "hash must be stable")

	otherMethod := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Other", nil, []byte{1, 2}, "")
	assert.NotEqual(t, base.RequestHash(), otherMethod.RequestHash())

	otherBody := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Method", nil, []byte{1, 3}, "")
	assert.NotEqual(t, base.RequestHash(), otherBody.RequestHash())

	labeled := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Method", nil, []byte{1, 2}, "",
		protocol.RequestLabelSelector{Name: "archive", Values: []string{"true"}})
	assert.NotEqual(t, base.RequestHash(), labeled.RequestHash(), "label selector key must fragment the hash")
}

func TestNewInternalUpstreamGrpcRequest(t *testing.T) {
	req := protocol.NewInternalUpstreamGrpcRequest("/pkg.Service/Method", []byte{7}, chains.POLYGON)

	assert.Equal(t, "/pkg.Service/Method", req.Method())
	assert.Equal(t, protocol.Grpc, req.RequestType())
	assert.NotEmpty(t, req.Id())
	assert.NotNil(t, req.RequestObserver())
	body, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, []byte{7}, body)
}

func TestUpstreamGrpcRequestIsSubscribeFollowsTheSpec(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	unary := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.LedgerService/GetObject", nil, nil, "sui")
	finite := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.LedgerService/ListCheckpoints", nil, nil, "sui")
	sub := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints", nil, nil, "sui")
	unknown := protocol.NewUpstreamGrpcRequest("1", "/sui.rpc.v2.LedgerService/Bogus", nil, nil, "sui")

	assert.False(t, unary.IsSubscribe())
	assert.True(t, finite.IsSubscribe())
	assert.True(t, sub.IsSubscribe())
	assert.False(t, unknown.IsSubscribe())
}
