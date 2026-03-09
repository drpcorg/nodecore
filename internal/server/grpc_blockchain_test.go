package server

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMapNativeSubscribeMethod(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	t.Run("uses native subscribe method as is", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", []byte(`["newHeads"]`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["newHeads"]`, string(payload))
	})

	t.Run("uses native subscribe method with default empty payload", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", nil)
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `[]`, string(payload))
	})

	t.Run("fails native subscribe method with invalid payload", func(te *testing.T) {
		_, _, err := mapNativeSubscribeMethod("eth", nil, "eth_subscribe", []byte(`not-json`))
		require.Error(te, err)
		assert.Contains(te, err.Error(), "invalid subscribe payload format")
	})

	t.Run("maps dproxy style method to eth_subscribe", func(te *testing.T) {
		method, payload, err := mapNativeSubscribeMethod("eth", nil, "newHeads", []byte(`[{"foo":"bar"}]`))
		require.NoError(te, err)
		assert.Equal(te, "eth_subscribe", method)
		assert.Equal(te, `["newHeads",{"foo":"bar"}]`, string(payload))
	})

	t.Run("returns unimplemented mapping error for unsupported method", func(te *testing.T) {
		_, _, err := mapNativeSubscribeMethod("solana", nil, "newHeads", nil)
		require.Error(te, err)
		assert.ErrorIs(te, err, errSubscribeMappingNotSupported)
	})
}

func TestExtractSubscriptionResultPayload(t *testing.T) {
	payload, isEvent, err := extractSubscriptionResultPayload([]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0x1","result":{"number":"0x1"}}}`))
	require.NoError(t, err)
	assert.True(t, isEvent)
	assert.Equal(t, `{"number":"0x1"}`, string(payload))

	payload, isEvent, err = extractSubscriptionResultPayload([]byte(`"0xsub"`))
	require.NoError(t, err)
	assert.False(t, isEvent)
	assert.Nil(t, payload)
}

func TestBuildNativeCallRequestsRestDataFail(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil)

	request := &dshackle.NativeCallRequest{
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "eth_call",
				Data: &dshackle.NativeCallItem_RestData{
					RestData: &dshackle.RestData{},
				},
			},
			{
				Id:     2,
				Method: "eth_chainId",
				Data: &dshackle.NativeCallItem_Payload{
					Payload: []byte(`[]`),
				},
			},
		},
	}

	requests, failures := service.buildNativeCallRequests(request, nil)
	require.Len(t, requests, 1)
	require.Len(t, failures, 1)
	assert.Equal(t, uint32(1), failures[0].GetId())
	assert.False(t, failures[0].GetSucceed())
	assert.Equal(t, int32(400), failures[0].GetItemErrorCode())
}

func TestNativeCallSuccessItemsChunking(t *testing.T) {
	items := nativeCallSuccessItems(7, "upstream-1", []byte("0123456789"), 4)
	require.Len(t, items, 3)

	assert.True(t, items[0].GetChunked())
	assert.False(t, items[0].GetFinalChunk())
	assert.Equal(t, "0123", string(items[0].GetPayload()))

	assert.True(t, items[1].GetChunked())
	assert.False(t, items[1].GetFinalChunk())
	assert.Equal(t, "4567", string(items[1].GetPayload()))

	assert.True(t, items[2].GetChunked())
	assert.True(t, items[2].GetFinalChunk())
	assert.Equal(t, "89", string(items[2].GetPayload()))
}

func TestNativeCallUnauthenticated(t *testing.T) {
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)))
	stream := &testNativeCallStream{ctx: context.Background()}

	err := service.NativeCall(&dshackle.NativeCallRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "no metadata")

	stream.ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("sessionid", "unknown"))
	err = service.NativeCall(&dshackle.NativeCallRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "does not exist")
}

func TestNativeSubscribeUnauthenticated(t *testing.T) {
	service := NewGrpcBlockchainService(nil, newGrpcSessionAuth(true, newGrpcSessionStore(time.Minute)))
	stream := &testNativeSubscribeStream{ctx: context.Background()}

	err := service.NativeSubscribe(&dshackle.NativeSubscribeRequest{}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "no metadata")
}

type testNativeCallStream struct {
	ctx  context.Context
	sent []*dshackle.NativeCallReplyItem
}

func (t *testNativeCallStream) Send(item *dshackle.NativeCallReplyItem) error {
	t.sent = append(t.sent, item)
	return nil
}

func (t *testNativeCallStream) SetHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeCallStream) SendHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeCallStream) SetTrailer(_ metadata.MD) {}

func (t *testNativeCallStream) Context() context.Context {
	return t.ctx
}

func (t *testNativeCallStream) SendMsg(_ any) error {
	return nil
}

func (t *testNativeCallStream) RecvMsg(_ any) error {
	return nil
}

type testNativeSubscribeStream struct {
	ctx  context.Context
	sent []*dshackle.NativeSubscribeReplyItem
}

func (t *testNativeSubscribeStream) Send(item *dshackle.NativeSubscribeReplyItem) error {
	t.sent = append(t.sent, item)
	return nil
}

func (t *testNativeSubscribeStream) SetHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeSubscribeStream) SendHeader(_ metadata.MD) error {
	return nil
}

func (t *testNativeSubscribeStream) SetTrailer(_ metadata.MD) {}

func (t *testNativeSubscribeStream) Context() context.Context {
	return t.ctx
}

func (t *testNativeSubscribeStream) SendMsg(_ any) error {
	return nil
}

func (t *testNativeSubscribeStream) RecvMsg(_ any) error {
	return nil
}
