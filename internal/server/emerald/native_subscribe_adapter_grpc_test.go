package emerald

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func suiChain() *chains.ConfiguredChain {
	return &chains.ConfiguredChain{Chain: chains.SUI, MethodSpec: "sui"}
}

func TestGrpcSubscribeAdapterBuildsGrpcRequest(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	adapter := grpcNativeSubscribeAdapter{}
	payload := []byte{0x0a, 0x03, 0x66, 0x6f, 0x6f} // arbitrary proto bytes, never inspected

	request, err := adapter.BuildRequest(suiChain(), nil, &dshackle.NativeSubscribeRequest{
		Method:   "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints",
		Payload:  payload,
		Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us"}}}},
		Data: &dshackle.NativeSubscribeRequest_GrpcData{GrpcData: &dshackle.GrpcSubRequestData{
			Metadata: []*dshackle.KeyValue{
				{Key: "x-api-key", Value: "secret"},
				{Key: "x-multi", Value: "a"},
				{Key: "x-multi", Value: "b"},
				{Key: "authorization", Value: "Bearer nodecore"}, // nodecore credential surface, must be dropped
			},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, protocol.Grpc, request.RequestType())
	assert.Equal(t, "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints", request.Method())
	assert.True(t, request.IsSubscribe())
	assert.Equal(t, "0", request.Id())
	body, err := request.Body()
	require.NoError(t, err)
	assert.Equal(t, payload, body)
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestLabelSelector{Name: "region", Values: []string{"us"}}}, request.Selectors())

	grpcRequest := request.(*protocol.UpstreamGrpcRequest)
	headers := grpcRequest.RequestParams().Headers
	assert.Equal(t, []string{"secret"}, headers["x-api-key"])
	assert.Equal(t, []string{"a", "b"}, headers["x-multi"])
	assert.NotContains(t, headers, "authorization")
}

func TestGrpcSubscribeAdapterAcceptsEmptyPayloadAndNoMetadata(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	adapter := grpcNativeSubscribeAdapter{}

	request, err := adapter.BuildRequest(suiChain(), nil, &dshackle.NativeSubscribeRequest{
		Method: "/sui.rpc.v2.LedgerService/ListCheckpoints",
	})
	require.NoError(t, err)

	body, err := request.Body()
	require.NoError(t, err)
	assert.Empty(t, body, "an empty message is a valid request")
	assert.Nil(t, request.(*protocol.UpstreamGrpcRequest).RequestParams().Headers)
	assert.Equal(t, []protocol.RequestSelector{protocol.RequestAnySelector{}}, request.Selectors())
}

func TestGrpcSubscribeAdapterRejectsNonStreamMethods(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	adapter := grpcNativeSubscribeAdapter{}

	for name, tc := range map[string]struct {
		chain  *chains.ConfiguredChain
		method string
	}{
		"json-rpc subscription on a json-rpc chain": {&chains.ConfiguredChain{Chain: chains.ETHEREUM, MethodSpec: "eth"}, "eth_subscribe"},
		"unknown grpc method":                       {suiChain(), "/sui.rpc.v2.Nope/Nope"},
		"unary grpc method":                         {suiChain(), "/sui.rpc.v2.LedgerService/GetServiceInfo"},
	} {
		t.Run(name, func(te *testing.T) {
			_, err := adapter.BuildRequest(tc.chain, nil, &dshackle.NativeSubscribeRequest{Method: tc.method})
			require.Error(te, err)
			assert.Equal(te, codes.Unimplemented, status.Code(err))
		})
	}
}

func grpcEvent(payload []byte, headers http.Header) *protocol.ResponseHolderWrapper {
	return &protocol.ResponseHolderWrapper{
		UpstreamId: "sui-1",
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEventResponse("0", payload).WithResponseHeaders(headers),
	}
}

func grpcEnd(trailers map[string][]string) *protocol.ResponseHolderWrapper {
	return &protocol.ResponseHolderWrapper{
		UpstreamId: "sui-1",
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEndResponse("0").WithResponseTrailers(trailers),
	}
}

func grpcUpstreamError(t *testing.T, code codes.Code, message string, headers http.Header, trailers map[string][]string) *protocol.ResponseHolderWrapper {
	st, err := status.New(code, message).WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(time.Second)})
	require.NoError(t, err)
	statusProto, err := proto.Marshal(st.Proto())
	require.NoError(t, err)
	respErr := protocol.NewGrpcStatusResponseError(&protocol.GrpcStatus{Code: code, Message: message, StatusProto: statusProto})
	return &protocol.ResponseHolderWrapper{
		UpstreamId: "sui-1",
		RequestId:  "0",
		Response:   protocol.NewReplyError("0", respErr, protocol.Grpc, protocol.TotalFailure).WithResponseHeaders(headers).WithResponseTrailers(trailers),
	}
}

func decodeStatus(t *testing.T, raw []byte) *status.Status {
	var statusProto spb.Status
	require.NoError(t, proto.Unmarshal(raw, &statusProto))
	return status.FromProto(&statusProto)
}

// drive feeds wrappers through the real loop with a never-firing heartbeat and
// returns the handler result plus what reached the client.
func drive(signer signature.ResponseSigner, nonce uint64, wrappers ...*protocol.ResponseHolderWrapper) ([]*dshackle.NativeSubscribeReplyItem, error) {
	stream := &testNativeSubscribeStream{ctx: context.Background()}
	responses := make(chan *protocol.ResponseHolderWrapper, len(wrappers))
	for _, wrapper := range wrappers {
		responses <- wrapper
	}
	close(responses)
	err := serveNativeSubscribe(stream, responses, grpcNativeSubscribeAdapter{}, nonce, signer, time.Hour)
	return stream.sent, err
}

func TestGrpcSubscribeAdapterFiniteStream(t *testing.T) {
	headers := http.Header{"x-node": {"sui-full"}}
	trailers := map[string][]string{"x-cost": {"3"}}

	items, err := drive(signature.NewDisabledSigner(), 0,
		grpcEvent([]byte("m1"), headers),
		grpcEvent([]byte("m2"), nil),
		grpcEvent([]byte("m3"), nil),
		grpcEnd(trailers),
	)
	require.NoError(t, err, "a clean end closes the NativeSubscribe stream with OK")
	require.Len(t, items, 4)

	for _, item := range items {
		assert.Equal(t, "sui-1", item.GetUpstreamId())
		assert.False(t, item.GetHeartbeat())
	}
	assert.Equal(t, "m1", string(items[0].GetPayload()))
	require.NotNil(t, items[0].GetGrpcData(), "the first item carries the upstream headers")
	assert.Equal(t, []*dshackle.KeyValue{{Key: "x-node", Value: "sui-full"}}, items[0].GetGrpcData().GetMetadata())
	assert.Empty(t, items[0].GetGrpcData().GetTrailers())
	assert.False(t, items[0].GetGrpcData().GetFinal())

	assert.Equal(t, "m2", string(items[1].GetPayload()))
	assert.Nil(t, items[1].GetGrpcData(), "a plain data item has nothing to say in grpc_data")
	assert.Nil(t, items[2].GetGrpcData())

	final := items[3]
	assert.Empty(t, final.GetPayload())
	assert.Nil(t, final.GetSignature())
	assert.True(t, final.GetGrpcData().GetFinal())
	assert.Empty(t, final.GetGrpcData().GetStatus(), "clean end has no status")
	assert.Equal(t, []*dshackle.KeyValue{{Key: "x-cost", Value: "3"}}, final.GetGrpcData().GetTrailers())
}

func TestGrpcSubscribeAdapterUpstreamErrorRidesInBand(t *testing.T) {
	trailers := map[string][]string{"retry-after": {"5"}}

	items, err := drive(signature.NewDisabledSigner(), 0,
		grpcEvent([]byte("m1"), nil),
		grpcUpstreamError(t, codes.ResourceExhausted, "slow down", nil, trailers),
		grpcEvent([]byte("never"), nil),
	)
	require.NoError(t, err, "the upstream status is in-band; the stream itself ends OK")
	require.Len(t, items, 2, "nothing after the final item")

	final := items[1]
	assert.True(t, final.GetGrpcData().GetFinal())
	assert.Empty(t, final.GetPayload())
	assert.Nil(t, final.GetSignature())
	assert.Equal(t, []*dshackle.KeyValue{{Key: "retry-after", Value: "5"}}, final.GetGrpcData().GetTrailers())

	st := decodeStatus(t, final.GetGrpcData().GetStatus())
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Equal(t, "slow down", st.Message())
	require.Len(t, st.Details(), 1)
	assert.IsType(t, &errdetails.RetryInfo{}, st.Details()[0])
}

func TestGrpcSubscribeAdapterZeroFrameFailureCarriesMetadataAndTrailers(t *testing.T) {
	items, err := drive(signature.NewDisabledSigner(), 0,
		grpcUpstreamError(t, codes.Unimplemented, "SubscriptionService disabled", http.Header{"x-node": {"sui-full"}}, map[string][]string{"x-cost": {"0"}}),
	)
	require.NoError(t, err)
	require.Len(t, items, 1)

	data := items[0].GetGrpcData()
	assert.True(t, data.GetFinal())
	assert.Equal(t, []*dshackle.KeyValue{{Key: "x-node", Value: "sui-full"}}, data.GetMetadata())
	assert.Equal(t, []*dshackle.KeyValue{{Key: "x-cost", Value: "0"}}, data.GetTrailers())
	assert.Equal(t, codes.Unimplemented, decodeStatus(t, data.GetStatus()).Code())
}

func TestGrpcSubscribeAdapterNodecoreFailureIsCanonicalStatus(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "sui-1",
		RequestId:  "0",
		Response:   protocol.NewReplyError("0", protocol.SubscribeTotalFailureError(), protocol.Grpc, protocol.TotalFailure),
	}

	items, err := drive(signature.NewDisabledSigner(), 0, wrapper)
	require.NoError(t, err)
	require.Len(t, items, 1)
	st := decodeStatus(t, items[0].GetGrpcData().GetStatus())
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Equal(t, "subscription total failure", st.Message())
}

func TestGrpcSubscribeAdapterEmptyPayloadIsNotFinal(t *testing.T) {
	items, err := drive(signature.NewDisabledSigner(), 0, grpcEvent([]byte{}, nil))
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Empty(t, items[0].GetPayload())
	assert.Nil(t, items[0].GetGrpcData(), "an empty message is data, not the end: no final marker")
}

func TestGrpcSubscribeAdapterSignsEventsButNeverTheFinalItem(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := signature.NewRSASigner(key)
	require.NoError(t, err)

	items, err := drive(signer, 42, grpcEvent([]byte("m1"), nil), grpcEnd(nil))
	require.NoError(t, err)
	require.Len(t, items, 2)

	require.NotNil(t, items[0].GetSignature())
	assert.Equal(t, uint64(42), items[0].GetSignature().GetNonce())
	assert.Equal(t, "sui-1", items[0].GetSignature().GetUpstreamId()) //nolint:staticcheck // SA1019: deprecated in proto, still populated by dshackle.
	assert.Nil(t, items[1].GetSignature())
}
