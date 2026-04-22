package flow

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyQuorumSignatures_NoRegistry_IsNoop(t *testing.T) {
	flow := &BaseExecutionFlow{quorumRegistry: nil}
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	resp := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	assert.False(t, wrapper.Response.HasError())
}

func TestVerifyQuorumSignatures_NoParamsInCtx_IsNoop(t *testing.T) {
	reg, _, _ := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}
	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	resp := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	flow.verifyQuorumSignatures(context.Background(), req, wrapper)

	assert.False(t, wrapper.Response.HasError())
}

func TestVerifyQuorumSignatures_ValidSignatures_Passthrough(t *testing.T) {
	reg, key, providerID := newTestSigner(t, "drpc-core@US-West#1")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	result := []byte(`"0xdeadbeef"`)
	headers := http.Header{}
	for i, nonce := range []uint64{11, 22} {
		upstreamID := fmt.Sprintf("node-%d", i)
		sig := signResult(t, key, nonce, upstreamID, result)
		headers.Set(
			fmt.Sprintf("QR%d-id-1", i),
			fmt.Sprintf("%s(%s)_nonce_%d_sig_0x%s", providerID, upstreamID, nonce, hex.EncodeToString(sig)),
		)
	}

	resp := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc).
		WithResponseHeaders(headers)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 2, QuorumOf: 3})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.False(t, wrapper.Response.HasError())
	assert.Equal(t, result, wrapper.Response.ResponseResult())
}

func TestVerifyQuorumSignatures_TamperedResult_ReplacesWithError(t *testing.T) {
	reg, key, providerID := newTestSigner(t, "drpc-core@US-West#1")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	signedOver := []byte(`"0xoriginal"`)
	sig := signResult(t, key, 1, "node-0", signedOver)

	headers := http.Header{}
	headers.Set("QR0-id-1",
		fmt.Sprintf("%s(node-0)_nonce_1_sig_0x%s", providerID, hex.EncodeToString(sig)))

	tamperedResult := []byte(`"0xtampered"`)
	resp := protocol.NewSimpleHttpUpstreamResponse("1", tamperedResult, protocol.JsonRpc).
		WithResponseHeaders(headers)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
}

func TestVerifyQuorumSignatures_UnknownProvider_ReplacesWithError(t *testing.T) {
	// Registry only knows about provider-b; the signed header claims provider-a.
	_, key, _ := newTestSigner(t, "provider-a")
	reg, _, _ := newTestSigner(t, "provider-b")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	result := []byte(`"0x1"`)
	sig := signResult(t, key, 1, "node-0", result)
	headers := http.Header{}
	headers.Set("QR0-id-1",
		fmt.Sprintf("provider-a(node-0)_nonce_1_sig_0x%s", hex.EncodeToString(sig)))

	resp := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc).
		WithResponseHeaders(headers)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
	assert.Contains(t, wrapper.Response.GetError().Message, "provider-a")
}

func TestVerifyQuorumSignatures_InsufficientSignatures_ReplacesWithError(t *testing.T) {
	reg, key, providerID := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	result := []byte(`"0x1"`)
	sig := signResult(t, key, 1, "node-0", result)
	headers := http.Header{}
	headers.Set("QR0-id-1",
		fmt.Sprintf("%s(node-0)_nonce_1_sig_0x%s", providerID, hex.EncodeToString(sig)))

	resp := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc).
		WithResponseHeaders(headers)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 3, QuorumOf: 5})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
	assert.Contains(t, wrapper.Response.GetError().Message, "got 1")
	assert.Contains(t, wrapper.Response.GetError().Message, "required 3")
}

func TestVerifyQuorumSignatures_MissingSignatures_ReplacesWithError(t *testing.T) {
	reg, _, _ := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	resp := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc).
		WithResponseHeaders(http.Header{"Content-Type": []string{"application/json"}})
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
}

func TestVerifyQuorumSignatures_RequestIDMismatch_ReplacesWithError(t *testing.T) {
	reg, key, providerID := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	result := []byte(`"0x1"`)
	sig := signResult(t, key, 1, "node-0", result)
	headers := http.Header{}
	// Request id in header is "other"; actual request id will be "1".
	headers.Set("QR0-id-other",
		fmt.Sprintf("%s(node-0)_nonce_1_sig_0x%s", providerID, hex.EncodeToString(sig)))

	resp := protocol.NewSimpleHttpUpstreamResponse("1", result, protocol.JsonRpc).
		WithResponseHeaders(headers)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
	assert.Contains(t, wrapper.Response.GetError().Message, "request id mismatch")
}

func TestVerifyQuorumSignatures_StreamResponse_RejectsAsNotSupported(t *testing.T) {
	reg, _, _ := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	// HttpUpstreamResponseStream has HasStream() == true regardless of the body.
	resp := protocol.NewHttpUpstreamResponseStream("1", strings.NewReader("stream-body"), protocol.JsonRpc)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, protocol.QuorumSignatureErrCode, wrapper.Response.GetError().Code)
	assert.Contains(t, wrapper.Response.GetError().Message, "not supported")
}

func TestVerifyQuorumSignatures_ResponseAlreadyHasError_SkipsVerification(t *testing.T) {
	reg, _, _ := newTestSigner(t, "provider-a")
	flow := &BaseExecutionFlow{quorumRegistry: reg}

	upstreamErr := protocol.ServerError()
	resp := protocol.NewReplyError("1", upstreamErr, protocol.JsonRpc, protocol.PartialFailure)
	wrapper := &protocol.ResponseHolderWrapper{RequestId: "1", Response: resp}

	req, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_test", nil, 0)
	ctx := quorum.WithParams(context.Background(), quorum.Params{Quorum: 1, QuorumOf: 1})
	flow.verifyQuorumSignatures(ctx, req, wrapper)

	require.True(t, wrapper.Response.HasError())
	assert.Equal(t, upstreamErr, wrapper.Response.GetError())
}

func newTestSigner(t *testing.T, providerID string) (*quorum.Registry, *ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	reg := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})
	return reg, key, providerID
}

func signResult(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, source string, message []byte) []byte {
	t.Helper()
	wrapped := quorum.WrapMessage(nonce, source, message)
	digest := sha256.Sum256(wrapped)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	require.NoError(t, err)
	return sig
}
