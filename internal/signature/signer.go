// Package signature signs responses that a gRPC client asked to have signed by
// putting a non-zero nonce on the request, reproducing dshackle's
// ResponseSigner (upstream/signature/RsaSigner.kt).
//
// The signed message format lives in internal/quorum, which implements the
// verifying half of the same scheme. Reusing quorum.WrapMessage here is
// deliberate: the two halves cannot drift apart.
package signature

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/drpcorg/nodecore/internal/quorum"
)

// ErrSigningNotConfigured is returned when a client requests a signature but no
// signing key is available.
//
//nolint:staticcheck // ST1005: capitalized on purpose
var ErrSigningNotConfigured = errors.New("Response signing requested via nonce but signing key is not configured")

type Signature struct {
	Value      []byte
	UpstreamID string
	KeyID      uint64
}

type ResponseSigner interface {
	// Enabled reports whether an actual signing key is configured.
	Enabled() bool
	// Sign returns a signature over `message` bound to `nonce` and `source`.
	Sign(nonce uint64, message []byte, source string) (Signature, error)
}

type disabledSigner struct{}

// NewDisabledSigner returns a signer that rejects every signing attempt. It is
// used when gRPC auth is off or no provider private key is configured.
func NewDisabledSigner() ResponseSigner {
	return disabledSigner{}
}

func (disabledSigner) Enabled() bool {
	return false
}

func (disabledSigner) Sign(uint64, []byte, string) (Signature, error) {
	return Signature{}, ErrSigningNotConfigured
}

type rsaSigner struct {
	key   *rsa.PrivateKey
	keyID uint64
}

func NewRSASigner(key *rsa.PrivateKey) (ResponseSigner, error) {
	keyID, err := KeyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &rsaSigner{key: key, keyID: keyID}, nil
}

func (s *rsaSigner) Enabled() bool {
	return true
}

func (s *rsaSigner) Sign(nonce uint64, message []byte, source string) (Signature, error) {
	digest := sha256.Sum256(quorum.WrapMessage(nonce, source, message))
	value, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return Signature{}, fmt.Errorf("unable to sign a response: %w", err)
	}
	return Signature{Value: value, UpstreamID: source, KeyID: s.keyID}, nil
}

func KeyID(pub *rsa.PublicKey) (uint64, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return 0, fmt.Errorf("unable to marshal a public key: %w", err)
	}
	digest := sha256.Sum256(der)
	return binary.BigEndian.Uint64(digest[:8]), nil
}
