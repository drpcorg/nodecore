package quorum_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapMessage_MatchesDshackleReference(t *testing.T) {
	act := quorum.WrapMessage(10, "infura", []byte("test"))

	// Reference vector from dshackle EcdsaSignerSpec."Wrap message".
	const want = "DSHACKLESIG/10/infura/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	assert.Equal(t, want, string(act))
}

func TestWrapMessage_NonceFitsUint64(t *testing.T) {
	// Real-world nonce larger than int64 max (see the easy2stake example).
	const nonce uint64 = 14484681713855751539
	msg := string(quorum.WrapMessage(nonce, "p", []byte("x")))
	assert.Contains(t, msg, "/14484681713855751539/")
}

func TestVerify_ValidSignature_ECDSA(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")

	result := []byte(`"0x1"`)
	const nonce uint64 = 1234
	sig := signResultECDSA(t, key, nonce, providerID, result)

	require.NoError(t, reg.Verify(providerID, providerID, nonce, sig, result))
}

func TestVerify_ValidSignature_RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const providerID = "easy2stake@EU-West#1"
	const upstreamID = "eth_vel-frank-n01"
	reg := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})

	result := []byte(`"0xabc"`)
	const nonce uint64 = 14484681713855751539
	sig := signResultRSA(t, key, nonce, upstreamID, result)

	require.NoError(t, reg.Verify(providerID, upstreamID, nonce, sig, result))
}

func TestVerify_UnknownProvider(t *testing.T) {
	reg, key, _ := newTestRegistry(t, "drpc-core@US-West#1")

	sig := signResultECDSA(t, key, 1, "other", []byte("x"))
	err := reg.Verify("other", "other", 1, sig, []byte("x"))
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrUnknownProvider)
}

func TestVerify_InvalidSignature_WrongMessage(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")

	sig := signResultECDSA(t, key, 1, providerID, []byte("original"))
	err := reg.Verify(providerID, providerID, 1, sig, []byte("tampered"))
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrInvalidSignature)
}

func TestVerify_InvalidSignature_WrongNonce(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")

	sig := signResultECDSA(t, key, 1, providerID, []byte("x"))
	err := reg.Verify(providerID, providerID, 2, sig, []byte("x"))
	assert.ErrorIs(t, err, quorum.ErrInvalidSignature)
}

func TestVerify_InvalidSignature_WrongSource(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")

	// Signed against source "other" but verified claiming source providerID.
	sig := signResultECDSA(t, key, 1, "other", []byte("x"))
	err := reg.Verify(providerID, providerID, 1, sig, []byte("x"))
	assert.ErrorIs(t, err, quorum.ErrInvalidSignature)
}

func TestVerifyHeaders_AllValid(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")
	result := []byte(`"0xabc"`)

	headers := http.Header{}
	for i, nonce := range []uint64{1, 2, 3} {
		upstreamID := fmt.Sprintf("node-%d", i)
		sig := signResultECDSA(t, key, nonce, upstreamID, result)
		headers.Set(
			fmt.Sprintf("QR%d-id-req42", i),
			qrHeaderValue(providerID, upstreamID, nonce, sig),
		)
	}

	require.NoError(t, reg.VerifyHeaders(headers, result, "req42", 3))
	require.NoError(t, reg.VerifyHeaders(headers, result, "", 0))
}

func TestVerifyHeaders_RequestIDMismatch(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "drpc-core@US-West#1")
	result := []byte(`"0xabc"`)

	headers := http.Header{}
	sig := signResultECDSA(t, key, 1, "u1", result)
	headers.Set("QR0-id-req42", qrHeaderValue(providerID, "u1", 1, sig))

	err := reg.VerifyHeaders(headers, result, "req-other", 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrUnexpectedRequestID)
}

func TestVerifyHeaders_WithUpstreamID(t *testing.T) {
	const providerID = "easy2stake@EU-West#1"
	const upstreamID = "eth_vel-frank-n01"
	prefix := providerID + "(" + upstreamID + ")"

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	reg := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})

	result := []byte(`"0xdeadbeef"`)
	const nonce uint64 = 14484681713855751539
	// Signature is computed against the upstream id only — the provider id
	// prefix is not part of the signed message.
	sig := signResultECDSA(t, key, nonce, upstreamID, result)

	headers := http.Header{}
	headers.Set("QR0-id-abc",
		fmt.Sprintf("%s_nonce_%d_sig_0x%s", prefix, nonce, hex.EncodeToString(sig)))

	require.NoError(t, reg.VerifyHeaders(headers, result, "abc", 1))

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, providerID, sigs[0].ProviderID)
	assert.Equal(t, upstreamID, sigs[0].UpstreamID)
	assert.Equal(t, nonce, sigs[0].Nonce)
}

func TestVerifyHeaders_ProviderPrefixIsNotSigned(t *testing.T) {
	const providerID = "easy2stake@EU-West#1"
	const upstreamID = "eth_vel-frank-n01"
	prefix := providerID + "(" + upstreamID + ")"

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	reg := quorum.NewRegistry(map[string]crypto.PublicKey{providerID: &key.PublicKey})

	result := []byte(`"0xdeadbeef"`)
	const nonce uint64 = 42
	// If we (incorrectly) signed the full `providerID(upstreamID)` prefix
	// the verifier must reject it, since dshackle only signs the upstream id.
	badSig := signResultECDSA(t, key, nonce, prefix, result)

	headers := http.Header{}
	headers.Set("QR0-id-abc",
		fmt.Sprintf("%s_nonce_%d_sig_0x%s", prefix, nonce, hex.EncodeToString(badSig)))

	err = reg.VerifyHeaders(headers, result, "abc", 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrInvalidSignature)
}

func TestVerifyHeaders_MissingSignatures(t *testing.T) {
	reg, _, _ := newTestRegistry(t, "p")

	err := reg.VerifyHeaders(http.Header{"Content-Type": []string{"application/json"}}, []byte("x"), "r", 1)
	assert.ErrorIs(t, err, quorum.ErrMissingSignatures)
}

func TestVerifyHeaders_InsufficientSignatures(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "p")
	result := []byte(`"x"`)

	sig := signResultECDSA(t, key, 1, "u1", result)
	headers := http.Header{}
	headers.Set("QR0-id-r", qrHeaderValue(providerID, "u1", 1, sig))

	err := reg.VerifyHeaders(headers, result, "r", 2)
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrInsufficientSignatures)
}

func TestVerifyHeaders_OneInvalidFailsAll(t *testing.T) {
	reg, key, providerID := newTestRegistry(t, "p")
	result := []byte(`"x"`)

	sigGood := signResultECDSA(t, key, 1, "u1", result)
	sigBad := signResultECDSA(t, key, 2, "u2", []byte("different"))

	headers := http.Header{}
	headers.Set("QR0-id-r", qrHeaderValue(providerID, "u1", 1, sigGood))
	headers.Set("QR1-id-r", qrHeaderValue(providerID, "u2", 2, sigBad))

	err := reg.VerifyHeaders(headers, result, "r", 2)
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrInvalidSignature)
}

func TestVerifyHeaders_UnknownProvider(t *testing.T) {
	reg, key, _ := newTestRegistry(t, "known")

	sig := signResultECDSA(t, key, 1, "u1", []byte("x"))
	headers := http.Header{}
	headers.Set("QR0-id-r", qrHeaderValue("stranger", "u1", 1, sig))

	err := reg.VerifyHeaders(headers, []byte("x"), "r", 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, quorum.ErrUnknownProvider)
}

func TestExtractSignatures_HeaderNameCaseInsensitive(t *testing.T) {
	headers := http.Header{}
	// bypass Go's canonicalisation to exercise the case-insensitive regex.
	headers["qr0-id-abc"] = []string{"pid(u1)_nonce_1_sig_0xaa"}

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, 0, sigs[0].Index)
	assert.Equal(t, "abc", sigs[0].RequestID)
	assert.Equal(t, "pid", sigs[0].ProviderID)
	assert.Equal(t, "u1", sigs[0].UpstreamID)
	assert.Equal(t, uint64(1), sigs[0].Nonce)
	assert.Equal(t, []byte{0xaa}, sigs[0].Raw)
}

func TestExtractSignatures_SortedByIndex(t *testing.T) {
	headers := http.Header{}
	headers.Set("QR2-id-r", "p(u)_nonce_3_sig_0x03")
	headers.Set("QR0-id-r", "p(u)_nonce_1_sig_0x01")
	headers.Set("QR1-id-r", "p(u)_nonce_2_sig_0x02")

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 3)
	for i, want := range []int{0, 1, 2} {
		assert.Equal(t, want, sigs[i].Index, "position %d", i)
	}
}

func TestExtractSignatures_MalformedHeaderValue(t *testing.T) {
	cases := map[string]string{
		"missing nonce separator": "pid(u)_sig_0xaa",
		"missing sig separator":   "pid(u)_nonce_1",
		"empty prefix":             "_nonce_1_sig_0xaa",
		"missing parens":           "pid_nonce_1_sig_0xaa",
		"empty provider id":        "(u)_nonce_1_sig_0xaa",
		"empty upstream id":        "pid()_nonce_1_sig_0xaa",
		"open paren only":          "pid(u_nonce_1_sig_0xaa",
		"close paren only":         "pidu)_nonce_1_sig_0xaa",
		"bad nonce":                "pid(u)_nonce_abc_sig_0xaa",
		"negative nonce":           "pid(u)_nonce_-1_sig_0xaa",
		"bad hex":                  "pid(u)_nonce_1_sig_0xzz",
		"empty signature":          "pid(u)_nonce_1_sig_",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("QR0-id-r", value)
			_, err := quorum.ExtractSignatures(headers)
			require.Error(t, err)
			assert.ErrorIs(t, err, quorum.ErrMalformedHeader)
		})
	}
}

func TestExtractSignatures_AcceptsProviderIdWithUnderscores(t *testing.T) {
	// The first "_nonce_" outside of the "(...)" segment delimits the prefix.
	headers := http.Header{}
	headers.Set("QR0-id-r", "my_weird_provider(some_node)_nonce_7_sig_0xbeef")

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "my_weird_provider", sigs[0].ProviderID)
	assert.Equal(t, "some_node", sigs[0].UpstreamID)
	assert.Equal(t, uint64(7), sigs[0].Nonce)
}

func TestExtractSignatures_SplitsUpstreamSuffix(t *testing.T) {
	headers := http.Header{}
	// Real-world shape from drpc astream.
	headers.Set("QR0-id-r", "easy2stake@EU-West#1(eth_vel-frank-n01)_nonce_7_sig_0xbeef")

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "easy2stake@EU-West#1", sigs[0].ProviderID)
	assert.Equal(t, "eth_vel-frank-n01", sigs[0].UpstreamID)
}

func TestExtractSignatures_SigWithoutHexPrefix(t *testing.T) {
	headers := http.Header{}
	headers.Set("QR0-id-r", "pid(u)_nonce_1_sig_abcd")

	sigs, err := quorum.ExtractSignatures(headers)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, []byte{0xab, 0xcd}, sigs[0].Raw)
}

func TestLoadRegistry_FromYAML_ECDSA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	yamlDoc := fmt.Sprintf(`keys:
  - provider_id: "drpc-core@US-West#1"
    public_key: "%s"
`, b64EncodePub(t, &key.PublicKey))

	reg, err := quorum.LoadRegistry([]byte(yamlDoc))
	require.NoError(t, err)
	assert.Equal(t, 1, reg.Len())
	pub, ok := reg.Lookup("drpc-core@US-West#1")
	require.True(t, ok)
	ec, isEC := pub.(*ecdsa.PublicKey)
	require.True(t, isEC)
	assert.True(t, ec.Equal(&key.PublicKey))
}

func TestLoadRegistry_FromYAML_RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	yamlDoc := fmt.Sprintf(`keys:
  - provider_id: "easy2stake@EU-West#1"
    public_key: "%s"
`, b64EncodePub(t, &key.PublicKey))

	reg, err := quorum.LoadRegistry([]byte(yamlDoc))
	require.NoError(t, err)
	pub, ok := reg.Lookup("easy2stake@EU-West#1")
	require.True(t, ok)
	rsaPub, isRSA := pub.(*rsa.PublicKey)
	require.True(t, isRSA)
	assert.True(t, rsaPub.Equal(&key.PublicKey))
}

func TestLoadRegistry_Errors(t *testing.T) {
	t.Run("bad yaml", func(t *testing.T) {
		_, err := quorum.LoadRegistry([]byte("keys: [this: is: invalid"))
		require.Error(t, err)
	})
	t.Run("empty provider_id", func(t *testing.T) {
		_, err := quorum.LoadRegistry([]byte("keys:\n  - provider_id: \"\"\n    public_key: \"x\"\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty provider_id")
	})
	t.Run("not a key", func(t *testing.T) {
		_, err := quorum.LoadRegistry([]byte("keys:\n  - provider_id: \"p\"\n    public_key: \"not a key\"\n"))
		require.Error(t, err)
	})
	t.Run("duplicate id", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		b64 := b64EncodePub(t, &key.PublicKey)
		yamlDoc := fmt.Sprintf(`keys:
  - provider_id: "dup"
    public_key: "%[1]s"
  - provider_id: "dup"
    public_key: "%[1]s"
`, b64)
		_, err = quorum.LoadRegistry([]byte(yamlDoc))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})
}

func TestLoadRegistry_AcceptsMultilineBase64(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	b64 := b64EncodePub(t, &key.PublicKey)

	var chunks []string
	for i := 0; i < len(b64); i += 32 {
		end := i + 32
		if end > len(b64) {
			end = len(b64)
		}
		chunks = append(chunks, "      "+b64[i:end])
	}
	yamlDoc := "keys:\n  - provider_id: \"p\"\n    public_key: |\n" +
		joinLines(chunks) + "\n"

	reg, err := quorum.LoadRegistry([]byte(yamlDoc))
	require.NoError(t, err)
	pub, ok := reg.Lookup("p")
	require.True(t, ok)
	ec, isEC := pub.(*ecdsa.PublicKey)
	require.True(t, isEC)
	assert.True(t, ec.Equal(&key.PublicKey))
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func TestDefaultRegistry_LoadsEmbeddedKeys(t *testing.T) {
	reg, err := quorum.DefaultRegistry()
	require.NoError(t, err)
	assert.Positive(t, reg.Len(), "embedded provider_keys.yaml should contain provider keys")

	_, ok := reg.Lookup("p2p-validator@EU-West#0")
	assert.True(t, ok, "Lookup should match fully-qualified <name>@<region>#<idx> provider ids")
}

func TestToResponseError(t *testing.T) {
	assert.Nil(t, quorum.ToResponseError(nil))

	assert.Equal(t,
		-32010,
		quorum.ToResponseError(quorum.ErrMissingSignatures).Code,
	)

	assert.Contains(t,
		quorum.ToResponseError(&quorum.InvalidSignatureError{ProviderID: "pid-x", Index: 0}).Message,
		"pid-x",
	)

	assert.Contains(t,
		quorum.ToResponseError(&quorum.UnknownProviderError{ProviderID: "pid-y"}).Message,
		"pid-y",
	)

	assert.Contains(t,
		quorum.ToResponseError(errors.New("random")).Message,
		"random",
	)

	insufficient := quorum.ToResponseError(&quorum.InsufficientSignaturesError{Got: 1, Required: 3})
	assert.Equal(t, -32010, insufficient.Code)
	assert.Contains(t, insufficient.Message, "got 1")
	assert.Contains(t, insufficient.Message, "required 3")

	notSupported := quorum.ToResponseError(&quorum.NotSupportedError{Reason: "no drpc upstreams"})
	assert.Equal(t, -32010, notSupported.Code)
	assert.Contains(t, notSupported.Message, "not supported")
	assert.Contains(t, notSupported.Message, "no drpc upstreams")

	unexpected := quorum.ToResponseError(&quorum.UnexpectedRequestIDError{Expected: "req1", Got: "req2"})
	assert.Equal(t, -32010, unexpected.Code)
	assert.Contains(t, unexpected.Message, "req1")
	assert.Contains(t, unexpected.Message, "req2")

	malformed := quorum.ToResponseError(&quorum.MalformedHeaderError{HeaderName: "QR0-id-x", Cause: errors.New("bad hex")})
	assert.Equal(t, -32010, malformed.Code)
	assert.Contains(t, malformed.Message, "malformed QR header")
}

func newTestRegistry(t *testing.T, providerID string) (*quorum.Registry, *ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	reg := quorum.NewRegistry(map[string]crypto.PublicKey{
		providerID: &key.PublicKey,
	})
	return reg, key, providerID
}

func qrHeaderValue(providerID, upstreamID string, nonce uint64, sig []byte) string {
	return fmt.Sprintf("%s(%s)_nonce_%d_sig_0x%s", providerID, upstreamID, nonce, hex.EncodeToString(sig))
}

func signResultECDSA(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, source string, message []byte) []byte {
	t.Helper()
	wrapped := quorum.WrapMessage(nonce, source, message)
	digest := sha256.Sum256(wrapped)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	require.NoError(t, err)
	return sig
}

func signResultRSA(t *testing.T, key *rsa.PrivateKey, nonce uint64, source string, message []byte) []byte {
	t.Helper()
	wrapped := quorum.WrapMessage(nonce, source, message)
	digest := sha256.Sum256(wrapped)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return sig
}

func b64EncodePub(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(der)
}
