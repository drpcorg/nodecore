package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func newTokenStrategyForTest(t *testing.T, secret string) auth.AuthRequestStrategy {
	t.Helper()
	authCfg := &config.AuthConfig{
		Enabled: true,
		RequestStrategyConfig: &config.RequestStrategyConfig{
			Type:                       config.Token,
			TokenRequestStrategyConfig: &config.TokenRequestStrategyConfig{Value: secret},
		},
	}
	strat, err := auth.NewAuthRequestStrategy(authCfg)
	assert.NoError(t, err)
	return strat
}

func TestTokenRequestStrategy_Success(t *testing.T) {
	// Arrange
	strat := newTokenStrategyForTest(t, "super-secret")

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set(auth.XNodecoreToken, "super-secret")
	payload := auth.NewHttpAuthPayload(req)

	// Act
	err := strat.AuthenticateRequest(context.Background(), payload)

	// Assert
	assert.NoError(t, err)
}

func TestTokenRequestStrategy_InvalidToken(t *testing.T) {
	// Arrange
	strat := newTokenStrategyForTest(t, "super-secret")

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set(auth.XNodecoreToken, "wrong-secret")
	payload := auth.NewHttpAuthPayload(req)

	// Act
	err := strat.AuthenticateRequest(context.Background(), payload)

	// Assert
	assert.ErrorContains(t, err, "invalid secret token")
}

func TestTokenRequestStrategy_MissingHeader(t *testing.T) {
	// Arrange
	strat := newTokenStrategyForTest(t, "super-secret")

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	// no header set
	payload := auth.NewHttpAuthPayload(req)

	// Act
	err := strat.AuthenticateRequest(context.Background(), payload)

	// Assert
	assert.ErrorContains(t, err, "invalid secret token")
}

func newNoopStrategyForTest(t *testing.T, enabled bool) auth.AuthRequestStrategy {
	t.Helper()
	authCfg := &config.AuthConfig{
		Enabled:               enabled,
		RequestStrategyConfig: nil, // this should yield the noopAuthRequestStrategy
	}
	strat, err := auth.NewAuthRequestStrategy(authCfg)
	assert.NoError(t, err)
	return strat
}

func TestNoopRequestStrategy_NoHeaders(t *testing.T) {
	// Arrange
	strat := newNoopStrategyForTest(t, true)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	// intentionally no headers
	payload := auth.NewHttpAuthPayload(req)

	// Act
	err := strat.AuthenticateRequest(context.Background(), payload)

	// Assert
	assert.NoError(t, err)
}

func TestTokenRequestStrategy_GrpcPayload_Success(t *testing.T) {
	strat := newTokenStrategyForTest(t, "super-secret")
	payload := auth.NewGrpcAuthPayload(metadata.Pairs(auth.XNodecoreToken, "super-secret"))

	assert.NoError(t, strat.AuthenticateRequest(context.Background(), payload))
}

// metadata keys are case-insensitive: any client casing lands lowercase on
// the wire and must still resolve the token
func TestTokenRequestStrategy_GrpcPayload_KeyIsCaseInsensitive(t *testing.T) {
	strat := newTokenStrategyForTest(t, "super-secret")
	payload := auth.NewGrpcAuthPayload(metadata.New(map[string]string{"X-NODECORE-TOKEN": "super-secret"}))

	assert.NoError(t, strat.AuthenticateRequest(context.Background(), payload))
}

func TestTokenRequestStrategy_GrpcPayload_InvalidToken(t *testing.T) {
	strat := newTokenStrategyForTest(t, "super-secret")
	payload := auth.NewGrpcAuthPayload(metadata.Pairs(auth.XNodecoreToken, "wrong-secret"))

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.ErrorContains(t, err, "invalid secret token")
}

func TestTokenRequestStrategy_GrpcPayload_MissingToken(t *testing.T) {
	strat := newTokenStrategyForTest(t, "super-secret")
	payload := auth.NewGrpcAuthPayload(metadata.MD{})

	err := strat.AuthenticateRequest(context.Background(), payload)
	assert.ErrorContains(t, err, "invalid secret token")
}
