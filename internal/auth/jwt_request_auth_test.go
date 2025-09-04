package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/dsheltie/internal/auth"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

// keyFormat enumerates how to encode the public key file for loadPubKey()
// supported formats per implementation: PKIX, PKCS1 (RSA), and x509 Certificate

type keyFormat int

const (
	pubPKIX keyFormat = iota
	pubPKCS1
	pubCert
)

// genRSAKey generates a temporary RSA private key for tests.
func genRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	return key
}

// writePubKeyFile writes the given RSA public key into a temp file in one of the supported formats
// and returns the file path. The file is created inside t.TempDir().
func writePubKeyFile(t *testing.T, pub *rsa.PublicKey, format keyFormat) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pubkey.pem")

	var pemBlock *pem.Block
	switch format {
	case pubPKIX:
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatalf("MarshalPKIXPublicKey err: %v", err)
		}
		pemBlock = &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	case pubPKCS1:
		der := x509.MarshalPKCS1PublicKey(pub)
		pemBlock = &pem.Block{Type: "RSA PUBLIC KEY", Bytes: der}
	case pubCert:
		// Not supported in this helper; use writeCertWithNewKey instead when a certificate is needed.
		t.Fatalf("pubCert requires writeCertWithNewKey; do not call writePubKeyFile with pubCert")
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp pub key file err: %v", err)
	}
	defer func() {
		closeErr := f.Close()
		if closeErr != nil {
			t.Fatalf("close temp pub key file err: %v", closeErr)
		}
	}()

	if err := pem.Encode(f, pemBlock); err != nil {
		t.Fatalf("pem.Encode err: %v", err)
	}
	return path
}

// bigIntOne is a helper big integer value of 1
var bigIntOne = big.NewInt(1)

// writeCertWithNewKey generates a new RSA key, issues a self-signed certificate, writes it to a temp file,
// and returns the path and the matching private key for signing JWTs.
func writeCertWithNewKey(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key := genRSAKey(t)

	tmpl := &x509.Certificate{
		SerialNumber:          bigIntOne,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate err: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create cert file err: %v", err)
	}
	defer func() {
		closeErr := f.Close()
		if closeErr != nil {
			t.Fatalf("close cert file err: %v", closeErr)
		}
	}()

	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatalf("encode cert pem err: %v", err)
	}

	return path, key
}

// newJWTStrategy constructs an AuthRequestStrategy configured for JWT with the given public key path and options.
func newJWTStrategy(t *testing.T, pubKeyPath string, allowedIssuer string, expRequired bool) auth.AuthRequestStrategy {
	t.Helper()
	cfg := &config.AuthConfig{
		Enabled: true,
		RequestStrategyConfig: &config.RequestStrategyConfig{
			Type: config.Jwt,
			JwtRequestStrategyConfig: &config.JwtRequestStrategyConfig{
				PublicKey:          pubKeyPath,
				AllowedIssuer:      allowedIssuer,
				ExpirationRequired: expRequired,
			},
		},
	}
	strat, err := auth.NewAuthRequestStrategy(cfg)
	if err != nil {
		t.Fatalf("NewAuthRequestStrategy err: %v", err)
	}
	return strat
}

// signRS256 creates a JWT string signed with RS256 using provided claims and private key.
func signRS256(t *testing.T, claims jwt.MapClaims, priv *rsa.PrivateKey) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	s, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("SignedString err: %v", err)
	}
	return s
}

func TestJWTStrategy_Success_PKIX(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)

	strat := newJWTStrategy(t, pubPath, "", false)

	claims := jwt.MapClaims{
		"sub": "user1",
		// no exp, allowed since expirationRequired=false
	}
	tokenStr := signRS256(t, claims, priv)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.NoError(t, err)
}

func TestJWTStrategy_Success_PKCS1(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKCS1)

	strat := newJWTStrategy(t, pubPath, "", false)

	tokenStr := signRS256(t, jwt.MapClaims{"foo": "bar"}, priv)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.NoError(t, err)
}

func TestJWTStrategy_Success_Certificate(t *testing.T) {
	certPath, priv := writeCertWithNewKey(t)
	strat := newJWTStrategy(t, certPath, "", false)

	tokenStr := signRS256(t, jwt.MapClaims{"foo": "bar"}, priv)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.NoError(t, err)
}

func TestJWTStrategy_MissingAuthorizationHeader(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)
	strat := newJWTStrategy(t, pubPath, "", false)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	// no Authorization header
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.ErrorContains(t, err, "missing Authorization header")
}

func TestJWTStrategy_InvalidAuthorizationFormat_NoToken(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)
	strat := newJWTStrategy(t, pubPath, "", false)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer ")
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.ErrorContains(t, err, "invalid Authorization header format")
}

func TestJWTStrategy_BearerWithExtraSpaces_StillParses(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)
	strat := newJWTStrategy(t, pubPath, "", false)

	tokenStr := signRS256(t, jwt.MapClaims{"foo": "bar"}, priv)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer    "+tokenStr) // spaces are trimmed
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.NoError(t, err)
}

func TestJWTStrategy_WrongSignature(t *testing.T) {
	// Public key A
	privA := genRSAKey(t)
	pubPathA := writePubKeyFile(t, &privA.PublicKey, pubPKIX)

	// Token signed by B
	privB := genRSAKey(t)

	strat := newJWTStrategy(t, pubPathA, "", false)

	tokenStr := signRS256(t, jwt.MapClaims{"foo": "bar"}, privB)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	payload := auth.NewHttpAuthPayload(req)

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.ErrorContains(t, err, "unable to parse jwt token:")
}

func TestJWTStrategy_AllowedIssuer_PassAndFail(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)

	// Allowed issuer set
	strat := newJWTStrategy(t, pubPath, "issuer-1", false)

	// Pass
	tokenOk := signRS256(t, jwt.MapClaims{"iss": "issuer-1"}, priv)
	req1, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req1.Header.Set("Authorization", "Bearer "+tokenOk)
	payload1 := auth.NewHttpAuthPayload(req1)
	err := strat.AuthenticateRequest(context.Background(), payload1)
	assert.NoError(t, err)

	// Fail (wrong iss)
	tokenBad := signRS256(t, jwt.MapClaims{"iss": "issuer-2"}, priv)
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenBad)
	payload2 := auth.NewHttpAuthPayload(req2)
	err = strat.AuthenticateRequest(context.Background(), payload2)
	assert.ErrorContains(t, err, "unable to parse jwt token:")
}

func TestJWTStrategy_ExpirationRequired_Various(t *testing.T) {
	priv := genRSAKey(t)
	pubPath := writePubKeyFile(t, &priv.PublicKey, pubPKIX)

	// Expiration required
	strat := newJWTStrategy(t, pubPath, "", true)

	// No exp -> fail
	tokenNoExp := signRS256(t, jwt.MapClaims{"foo": "bar"}, priv)
	req1, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req1.Header.Set("Authorization", "Bearer "+tokenNoExp)
	payload1 := auth.NewHttpAuthPayload(req1)
	err := strat.AuthenticateRequest(context.Background(), payload1)
	assert.ErrorContains(t, err, "unable to parse jwt token:")

	// Exp in future -> pass
	future := time.Now().Add(10 * time.Minute).Unix()
	tokenFuture := signRS256(t, jwt.MapClaims{"exp": future}, priv)
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenFuture)
	payload2 := auth.NewHttpAuthPayload(req2)
	err = strat.AuthenticateRequest(context.Background(), payload2)
	assert.NoError(t, err)

	// Expired -> fail
	past := time.Now().Add(-10 * time.Minute).Unix()
	tokenPast := signRS256(t, jwt.MapClaims{"exp": past}, priv)
	req3, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req3.Header.Set("Authorization", "Bearer "+tokenPast)
	payload3 := auth.NewHttpAuthPayload(req3)
	err = strat.AuthenticateRequest(context.Background(), payload3)
	assert.ErrorContains(t, err, "unable to parse jwt token:")
}
