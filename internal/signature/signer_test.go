package signature_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"testing"

	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// dshackle derives the key id with ByteBuffer.wrap(sha256(pub)).asLongBuffer().get(),
// i.e. the first 8 bytes big-endian. Assert the property rather than a magic
// constant so the test explains what compatibility actually requires.
func TestKeyIDIsBigEndianPrefixOfPublicKeyDigest(t *testing.T) {
	key := testKey(t)

	keyID, err := signature.KeyID(&key.PublicKey)
	require.NoError(t, err)

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	digest := sha256.Sum256(der)

	assert.Equal(t, binary.BigEndian.Uint64(digest[:8]), keyID)
}

// The load-bearing test: internal/quorum was written against the dshackle spec
// independently of this signer, so a green round trip means the wrap format,
// the digest and the padding all match dshackle.
func TestRSASignerRoundTripsThroughQuorumVerifier(t *testing.T) {
	key := testKey(t)
	signer, err := signature.NewRSASigner(key)
	require.NoError(t, err)
	assert.True(t, signer.Enabled())

	const providerID = "nodecore@EU-West#0"
	const upstreamID = "eth-upstream-1"
	const nonce uint64 = 1234
	result := []byte(`"0x1"`)

	sig, err := signer.Sign(nonce, result, upstreamID)
	require.NoError(t, err)
	assert.NotEmpty(t, sig.Value)

	registry := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})
	require.NoError(t, registry.Verify(providerID, upstreamID, nonce, sig.Value, result))
}

// dshackle needs Long.toUnsignedString for this; Go's uint64 gets it for free.
// Pinned so nobody "simplifies" the nonce to an int64 later.
func TestRSASignerHandlesNonceAboveInt64Max(t *testing.T) {
	key := testKey(t)
	signer, err := signature.NewRSASigner(key)
	require.NoError(t, err)

	const providerID = "nodecore@EU-West#0"
	const upstreamID = "eth-upstream-1"
	const nonce uint64 = 14484681713855751539
	result := []byte(`"0xabc"`)

	sig, err := signer.Sign(nonce, result, upstreamID)
	require.NoError(t, err)

	registry := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})
	require.NoError(t, registry.Verify(providerID, upstreamID, nonce, sig.Value, result))
}

func TestRSASignerSignatureDoesNotVerifyAfterTampering(t *testing.T) {
	key := testKey(t)
	signer, err := signature.NewRSASigner(key)
	require.NoError(t, err)

	const providerID = "nodecore@EU-West#0"
	const upstreamID = "eth-upstream-1"
	const nonce uint64 = 77
	result := []byte(`"0x1"`)

	sig, err := signer.Sign(nonce, result, upstreamID)
	require.NoError(t, err)

	registry := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})

	assert.Error(t, registry.Verify(providerID, upstreamID, nonce, sig.Value, []byte(`"0x2"`)),
		"a different result must not verify")
	assert.Error(t, registry.Verify(providerID, "other-upstream", nonce, sig.Value, result),
		"a different source must not verify")
	assert.Error(t, registry.Verify(providerID, upstreamID, nonce+1, sig.Value, result),
		"a different nonce must not verify")
}

func TestDisabledSignerRejectsSigning(t *testing.T) {
	signer := signature.NewDisabledSigner()

	assert.False(t, signer.Enabled())

	_, err := signer.Sign(1, []byte(`"0x1"`), "eth-upstream-1")
	require.ErrorIs(t, err, signature.ErrSigningNotConfigured)
}
