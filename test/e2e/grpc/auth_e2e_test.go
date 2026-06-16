//go:build e2e

package grpc_e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCAuthRequiredBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-grpc-auth", 0)
	defer upstream.Terminate(ctx)

	providerKey := generateGrpcAuthRSAKey(t)
	externalKey := generateGrpcAuthRSAKey(t)
	files := map[string]string{
		"/tmp/nodecore-e2e-grpc-provider.pem": grpcAuthPrivateKeyPEM(t, providerKey),
		"/tmp/nodecore-e2e-grpc-external.pub": grpcAuthPublicKeyPEM(t, &externalKey.PublicKey),
	}
	nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, grpcAuthNodecoreConfig(upstream), files)
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	authClient := dshackle.NewAuthClient(conn)
	chain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	call := harness.NativeCall{Chain: chain, ID: 501, Method: "eth_blockNumber", Payload: `[]`}

	_, unauthErr := harness.NativeCallOnce(ctx, client, call, 5*time.Second)
	if unauthErr == nil || status.Code(unauthErr) != codes.Unauthenticated {
		t.Fatalf("NativeCall without session error=%v code=%s want Unauthenticated\nlogs:\n%s", unauthErr, status.Code(unauthErr), nodecore.Logs(ctx))
	}

	incomingToken := signGrpcAuthIncomingToken(t, externalKey, "drpc-e2e", time.Now().Unix(), "V1")
	authResp, err := authClient.Authenticate(ctx, &dshackle.AuthRequest{Token: incomingToken})
	if err != nil {
		t.Fatalf("Authenticate failed: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}
	sessionID := extractGrpcAuthSessionID(t, authResp.GetProviderToken(), &providerKey.PublicKey)
	if sessionID == "" {
		t.Fatalf("Authenticate returned empty session id token=%q", authResp.GetProviderToken())
	}

	item := harness.NativeCallUntilSuccess(t, harness.GRPCSessionContext(ctx, sessionID), client, nodecore, call, 30*time.Second)
	if item.GetId() != 501 || item.GetUpstreamId() == "" {
		t.Fatalf("authenticated NativeCall returned unexpected item: %+v\nlogs:\n%s", item, nodecore.Logs(ctx))
	}
}

func grpcAuthNodecoreConfig(upstream *harness.RPCNode) string {
	return fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: true
    public-key-owner: drpc-e2e
    provider-private-key-path: /tmp/nodecore-e2e-grpc-provider.pem
    external-public-key-path: /tmp/nodecore-e2e-grpc-external.pub
    session-ttl: 1m
upstream-config:
  mode: default
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
    - id: %s
      chain: ethereum
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL())
}

func generateGrpcAuthRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func grpcAuthPrivateKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func grpcAuthPublicKeyPEM(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyBytes}))
}

func signGrpcAuthIncomingToken(t *testing.T, key *rsa.PrivateKey, issuer string, issuedAt int64, version string) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": issuer, "iat": issuedAt, "version": version}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign incoming auth token: %v", err)
	}
	return signed
}

func extractGrpcAuthSessionID(t *testing.T, token string, key *rsa.PublicKey) string {
	t.Helper()
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	if err != nil {
		t.Fatalf("parse provider auth token: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("provider auth token is invalid")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("provider auth token claims have unexpected type")
	}
	sessionID, _ := claims["sessionId"].(string)
	return sessionID
}
