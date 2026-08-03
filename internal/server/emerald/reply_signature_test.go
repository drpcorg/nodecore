package emerald

import (
	"context"
	"crypto"
	"crypto/rsa"
	"fmt"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseSignerDisabledWithoutGrpcAuth(t *testing.T) {
	signer, err := newResponseSigner(nil)
	require.NoError(t, err)
	assert.False(t, signer.Enabled())

	signer, err = newResponseSigner(&config.GrpcAuthConfig{Enabled: false})
	require.NoError(t, err)
	assert.False(t, signer.Enabled())
}

func TestNewResponseSignerDisabledWithoutPrivateKeyPath(t *testing.T) {
	signer, err := newResponseSigner(&config.GrpcAuthConfig{Enabled: true})
	require.NoError(t, err)
	assert.False(t, signer.Enabled())
}

func TestNewResponseSignerLoadsConfiguredKey(t *testing.T) {
	key := generateRSAKey(t)
	path := writePrivateKeyPEM(t, key)

	signer, err := newResponseSigner(&config.GrpcAuthConfig{
		Enabled:                true,
		ProviderPrivateKeyPath: path,
	})
	require.NoError(t, err)
	assert.True(t, signer.Enabled())
}

func TestNewResponseSignerFailsOnUnreadableKey(t *testing.T) {
	_, err := newResponseSigner(&config.GrpcAuthConfig{
		Enabled:                true,
		ProviderPrivateKeyPath: "/nonexistent/private.pem",
	})
	require.Error(t, err)
}

func testSigner(t *testing.T) (signature.ResponseSigner, *rsa.PublicKey) {
	t.Helper()
	key := generateRSAKey(t)
	signer, err := signature.NewRSASigner(key)
	require.NoError(t, err)
	return signer, &key.PublicKey
}

func mustTestSigner(t *testing.T) signature.ResponseSigner {
	t.Helper()
	signer, _ := testSigner(t)
	return signer
}

// A signed reply must verify with internal/quorum, the same code that checks
// dshackle's signatures. `source` is the upstream id.
func assertVerifies(t *testing.T, pub *rsa.PublicKey, sig *dshackle.NativeCallReplySignature, source string, result []byte) {
	t.Helper()
	const providerID = "nodecore-test"
	registry := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: pub})
	require.NoError(t, registry.Verify(providerID, source, sig.GetNonce(), sig.GetSignature(), result))
}

func TestSendReplySignsBufferedSuccessWhenNonceRequested(t *testing.T) {
	signer, pub := testSigner(t)
	result := []byte(`"0x1"`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 42, signer))
	require.Len(t, stream.sent, 1)

	sig := stream.sent[0].GetSignature()
	require.NotNil(t, sig)
	assert.Equal(t, uint64(42), sig.GetNonce())
	assert.Equal(t, "upstream-1", sig.GetUpstreamId()) //nolint:staticcheck // SA1019: deprecated in proto, still populated for dshackle parity.
	assert.NotZero(t, sig.GetKeyId())
	assertVerifies(t, pub, sig, "upstream-1", result)
}

func TestSendReplyDoesNotSignWithoutNonce(t *testing.T) {
	signer, _ := testSigner(t)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 0, signer))
	require.Len(t, stream.sent, 1)
	assert.Nil(t, stream.sent[0].GetSignature())
}

// Cache hits and locally-served methods carry the literal "NoUpstream". They
// are signed as-is rather than skipped - see decision 2 in the design spec.
func TestSendReplySignsCacheHitWithNoUpstreamSource(t *testing.T) {
	signer, pub := testSigner(t)
	result := []byte(`"0xcached"`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 7, signer))
	require.Len(t, stream.sent, 1)

	sig := stream.sent[0].GetSignature()
	require.NotNil(t, sig)
	assert.Equal(t, flow.NoUpstream, sig.GetUpstreamId()) //nolint:staticcheck // SA1019: deprecated in proto, still populated for dshackle parity.
	assertVerifies(t, pub, sig, flow.NoUpstream, result)
}

func TestSendReplyDoesNotSignErrorReplies(t *testing.T) {
	signer, _ := testSigner(t)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-err",
		RequestId:  "1",
		Response:   protocol.NewHttpUpstreamResponseWithError(protocol.ServerErrorWithCause(fmt.Errorf("boom"))),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 42, signer))
	require.Len(t, stream.sent, 1)
	assert.False(t, stream.sent[0].GetSucceed())
	assert.Nil(t, stream.sent[0].GetSignature())
}

// dshackle never calls its signer on the stream path (QuorumRequestReader.kt:161,
// "TODO: do streaming signature"), so a streamed reply goes out unsigned rather
// than failing - even with a disabled signer. The assertion is deliberately
// about the absence of a signature on every emitted item, so it holds whether
// the stream unwraps cleanly or resolves into an error item.
func TestSendReplyLeavesStreamedResponsesUnsignedAndDoesNotFail(t *testing.T) {
	signers := map[string]signature.ResponseSigner{
		"enabled":  mustTestSigner(t),
		"disabled": signature.NewDisabledSigner(),
	}

	for name, signer := range signers {
		t.Run(name, func(te *testing.T) {
			body := strings.NewReader(`{"jsonrpc":"2.0","id":"0","result":"0x1"}`)
			wrapper := &protocol.ResponseHolderWrapper{
				UpstreamId: "upstream-1",
				RequestId:  "1",
				Response:   protocol.NewHttpUpstreamResponseStream("1", body, protocol.JsonRpc),
			}
			stream := &testNativeCallStream{ctx: context.Background()}

			require.NoError(te, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 42, signer))
			require.NotEmpty(te, stream.sent)
			for _, item := range stream.sent {
				assert.Nil(te, item.GetSignature())
			}
		})
	}
}

// A client that asked for a signature must never get a silently unsigned
// result - dshackle's DisabledSigner throws and the item comes back as an error.
func TestSendReplyFailsItemWhenSigningRequestedButUnavailable(t *testing.T) {
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "1",
		Response:   protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	}
	stream := &testNativeCallStream{ctx: context.Background()}

	require.NoError(t, jsonRpcNativeCallAdapter{}.SendReply(stream, wrapper, 42, signature.NewDisabledSigner()))
	require.Len(t, stream.sent, 1)

	assert.False(t, stream.sent[0].GetSucceed())
	assert.Nil(t, stream.sent[0].GetSignature())
	assert.Contains(t, stream.sent[0].GetErrorMessage(), "signing key is not configured")
}

func TestBuildNativeCallRequestsCarriesPerItemNonce(t *testing.T) {
	service := NewGrpcBlockchainService(nil, nil, signature.NewDisabledSigner())
	configuredChain := &chains.ConfiguredChain{MethodSpec: "eth"}

	_, items, preResponses := service.buildNativeCallRequests(configuredChain, &dshackle.NativeCallRequest{
		Items: []*dshackle.NativeCallItem{
			{
				Id:     1,
				Method: "eth_blockNumber",
				Nonce:  99,
				Data:   &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)},
			},
			{
				Id:     2,
				Method: "eth_blockNumber",
				Data:   &dshackle.NativeCallItem_Payload{Payload: []byte(`[]`)},
			},
		},
	})
	require.Empty(t, preResponses)
	require.Len(t, items, 2)

	assert.Equal(t, uint64(99), items["1"].nonce)
	assert.Equal(t, uint64(0), items["2"].nonce)
}

func TestNativeSubscribeReplyItemSignsEventWhenNonceRequested(t *testing.T) {
	signer, pub := testSigner(t)
	event := []byte(`{"number":"0x1"}`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEventResponse("0", event),
	}

	item, err := nativeSubscribeReplyItem(wrapper, event, 55, signer)
	require.NoError(t, err)

	assert.Equal(t, event, item.GetPayload())
	assert.Equal(t, "upstream-1", item.GetUpstreamId())

	sig := item.GetSignature()
	require.NotNil(t, sig)
	assert.Equal(t, uint64(55), sig.GetNonce())
	assertVerifies(t, pub, sig, "upstream-1", event)
}

func TestNativeSubscribeReplyItemUnsignedWithoutNonce(t *testing.T) {
	signer, _ := testSigner(t)
	event := []byte(`{"number":"0x1"}`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEventResponse("0", event),
	}

	item, err := nativeSubscribeReplyItem(wrapper, event, 0, signer)
	require.NoError(t, err)
	assert.Nil(t, item.GetSignature())
}

func TestNativeSubscribeReplyItemFailsWhenSigningUnavailable(t *testing.T) {
	event := []byte(`{"number":"0x1"}`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: "upstream-1",
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEventResponse("0", event),
	}

	_, err := nativeSubscribeReplyItem(wrapper, event, 55, signature.NewDisabledSigner())
	require.ErrorIs(t, err, signature.ErrSigningNotConfigured)
}

// Locally-synthesized events carry "NoUpstream" and are signed with it as the
// source - see decision 2 in the design spec.
func TestNativeSubscribeReplyItemSignsLocalEventWithNoUpstreamSource(t *testing.T) {
	signer, pub := testSigner(t)
	event := []byte(`{"number":"0x2"}`)
	wrapper := &protocol.ResponseHolderWrapper{
		UpstreamId: flow.NoUpstream,
		RequestId:  "0",
		Response:   protocol.NewSubscriptionEventResponse("0", event),
	}

	item, err := nativeSubscribeReplyItem(wrapper, event, 3, signer)
	require.NoError(t, err)

	sig := item.GetSignature()
	require.NotNil(t, sig)
	assert.Equal(t, flow.NoUpstream, sig.GetUpstreamId()) //nolint:staticcheck // SA1019: deprecated in proto, still populated for dshackle parity.
	assertVerifies(t, pub, sig, flow.NoUpstream, event)
}
