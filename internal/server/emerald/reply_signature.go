package emerald

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/dshackle"
)

// newResponseSigner builds the response signer from the gRPC auth config.
// Signing is available exactly when gRPC auth is enabled with a provider
// private key - the same condition dshackle's ResponseSignerFactory uses.
//
// This re-reads the PEM that NewGrpcAuthService also reads. One extra read at
// startup keeps the auth service and the signer independent of each other.
func newResponseSigner(cfg *config.GrpcAuthConfig) (signature.ResponseSigner, error) {
	if cfg.Disabled() {
		return signature.NewDisabledSigner(), nil
	}

	key, err := loadRSAPrivateKeyFromFile(cfg.ProviderPrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to load the grpc provider private key for response signing: %w", err)
	}
	return signature.NewRSASigner(key)
}

// buildReplySignature signs `result` when the client requested a signature with
// a non-zero nonce, and returns (nil, nil) when it did not. `source` is the
// upstream id the result came from, which is what the signature binds to.
func buildReplySignature(
	signer signature.ResponseSigner,
	nonce uint64,
	result []byte,
	source string,
) (*dshackle.NativeCallReplySignature, error) {
	if nonce == 0 {
		return nil, nil
	}

	sig, err := signer.Sign(nonce, result, source)
	if err != nil {
		return nil, err
	}

	return &dshackle.NativeCallReplySignature{
		Nonce:      nonce,
		Signature:  sig.Value,
		KeyId:      sig.KeyID,
		UpstreamId: sig.UpstreamID, //nolint:staticcheck // SA1019: deprecated in proto, still populated by dshackle.
	}, nil
}

// nativeSubscribeReplyItem builds one subscription event reply, signing the
// payload when the client requested it with a non-zero nonce on the subscribe
// request.
func nativeSubscribeReplyItem(
	wrapper *protocol.ResponseHolderWrapper,
	result []byte,
	nonce uint64,
	signer signature.ResponseSigner,
) (*dshackle.NativeSubscribeReplyItem, error) {
	replySignature, err := buildReplySignature(signer, nonce, result, wrapper.UpstreamId)
	if err != nil {
		return nil, err
	}

	return &dshackle.NativeSubscribeReplyItem{
		Payload:    result,
		UpstreamId: wrapper.UpstreamId,
		Signature:  replySignature,
	}, nil
}
