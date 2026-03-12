package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGrpcAuthServiceAuthenticateSuccess(t *testing.T) {
	providerPrivateKey := generateRSAKey(t)
	externalPrivateKey := generateRSAKey(t)

	cfg := &config.GrpcAuthConfig{
		Enabled:                true,
		PublicKeyOwner:         "drpc",
		ProviderPrivateKeyPath: writePrivateKeyPEM(t, providerPrivateKey),
		ExternalPublicKeyPath:  writePublicKeyPEM(t, &externalPrivateKey.PublicKey),
		SessionTTL:             time.Minute,
	}

	service, sessionAuth, err := NewGrpcAuthService(cfg)
	require.NoError(t, err)

	requestToken := signIncomingToken(t, externalPrivateKey, "drpc", time.Now().Unix(), grpcAuthVersionV1)
	response, err := service.Authenticate(context.Background(), &dshackle.AuthRequest{Token: requestToken})
	require.NoError(t, err)
	require.NotEmpty(t, response.GetProviderToken())

	sessionID := extractSessionIDClaim(t, response.GetProviderToken(), &providerPrivateKey.PublicKey)
	require.NotEmpty(t, sessionID)

	sessionCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("sessionid", sessionID))
	assert.NoError(t, sessionAuth.requireSession(sessionCtx))
}

func TestGrpcAuthServiceAuthenticateDisabled(t *testing.T) {
	service, sessionAuth, err := NewGrpcAuthService(&config.GrpcAuthConfig{
		Enabled: false,
	})
	require.NoError(t, err)
	assert.Nil(t, service)
	assert.Nil(t, sessionAuth)
}

func TestGrpcAuthServiceAuthenticateInvalidTokens(t *testing.T) {
	providerPrivateKey := generateRSAKey(t)
	externalPrivateKey := generateRSAKey(t)

	cfg := &config.GrpcAuthConfig{
		Enabled:                true,
		PublicKeyOwner:         "drpc",
		ProviderPrivateKeyPath: writePrivateKeyPEM(t, providerPrivateKey),
		ExternalPublicKeyPath:  writePublicKeyPEM(t, &externalPrivateKey.PublicKey),
		SessionTTL:             time.Minute,
	}

	service, _, err := NewGrpcAuthService(cfg)
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "missing version",
			token: signIncomingToken(t, externalPrivateKey, "drpc", time.Now().Unix(), ""),
		},
		{
			name:  "wrong issuer",
			token: signIncomingToken(t, externalPrivateKey, "another", time.Now().Unix(), grpcAuthVersionV1),
		},
		{
			name:  "stale iat",
			token: signIncomingToken(t, externalPrivateKey, "drpc", time.Now().Add(-2*time.Minute).Unix(), grpcAuthVersionV1),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			_, authErr := service.Authenticate(context.Background(), &dshackle.AuthRequest{Token: test.token})
			require.Error(te, authErr)
			assert.Equal(te, codes.InvalidArgument, status.Code(authErr))
		})
	}
}

func TestGrpcAuthServiceAuthenticateNilRequest(t *testing.T) {
	providerPrivateKey := generateRSAKey(t)
	externalPrivateKey := generateRSAKey(t)

	cfg := &config.GrpcAuthConfig{
		Enabled:                true,
		PublicKeyOwner:         "drpc",
		ProviderPrivateKeyPath: writePrivateKeyPEM(t, providerPrivateKey),
		ExternalPublicKeyPath:  writePublicKeyPEM(t, &externalPrivateKey.PublicKey),
		SessionTTL:             time.Minute,
	}

	service, _, err := NewGrpcAuthService(cfg)
	require.NoError(t, err)

	_, authErr := service.Authenticate(context.Background(), nil)
	require.Error(t, authErr)
	assert.Equal(t, codes.InvalidArgument, status.Code(authErr))
	assert.Contains(t, authErr.Error(), "Invalid request: request is nil")
}

func TestGrpcSessionStoreTTL(t *testing.T) {
	store := newGrpcSessionStore(40 * time.Millisecond)
	store.Put("session-1")

	assert.True(t, store.Exists("session-1"))
	time.Sleep(30 * time.Millisecond)
	assert.True(t, store.Exists("session-1")) // extends ttl on access
	time.Sleep(30 * time.Millisecond)
	assert.True(t, store.Exists("session-1"))
	time.Sleep(60 * time.Millisecond)
	assert.False(t, store.Exists("session-1"))
}

func TestGrpcSessionAuthRequireSession(t *testing.T) {
	store := newGrpcSessionStore(time.Minute)
	store.Put("ok")

	authEnabled := newGrpcSessionAuth(true, store)
	authDisabled := newGrpcSessionAuth(false, store)

	err := authEnabled.requireSession(context.Background())
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "no metadata")

	invalidCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("sessionid", "missing"))
	err = authEnabled.requireSession(invalidCtx)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Contains(t, err.Error(), "does not exist")

	validCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("sessionid", "ok"))
	assert.NoError(t, authEnabled.requireSession(validCtx))
	assert.NoError(t, authDisabled.requireSession(context.Background()))
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func writePrivateKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	content := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	path := filepath.Join(t.TempDir(), "private.pem")
	require.NoError(t, os.WriteFile(path, content, 0600))
	return path
}

func writePublicKeyPEM(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(key)
	require.NoError(t, err)

	content := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})
	path := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(path, content, 0600))
	return path
}

func signIncomingToken(t *testing.T, key *rsa.PrivateKey, issuer string, issuedAt int64, version string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer,
		"iat": issuedAt,
	}
	if version != "" {
		claims[grpcAuthVersionClaim] = version
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func extractSessionIDClaim(t *testing.T, token string, key *rsa.PublicKey) string {
	t.Helper()
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	sessionID, ok := claims[grpcAuthSessionIDClaim].(string)
	require.True(t, ok)
	return sessionID
}
