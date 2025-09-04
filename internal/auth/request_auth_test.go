package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/drpcorg/dsheltie/internal/auth"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/stretchr/testify/assert"
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
	req.Header.Set(auth.XDsheltieToken, "super-secret")
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
	req.Header.Set(auth.XDsheltieToken, "wrong-secret")
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
