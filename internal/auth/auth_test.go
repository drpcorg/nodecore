package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

// helper to construct a basic auth processor with one local key and token strategy
func newBasicProcessor(t *testing.T, token string, allowedIps []string, methods *config.AuthMethods, contracts *config.AuthContracts) auth.AuthProcessor {
	t.Helper()
	appCfg := &config.AuthConfig{
		Enabled: true,
		RequestStrategyConfig: &config.RequestStrategyConfig{
			Type:                       config.Token,
			TokenRequestStrategyConfig: &config.TokenRequestStrategyConfig{Value: token},
		},
		KeyConfigs: []*config.KeyConfig{
			{
				Id:   "k1",
				Type: config.LocalKey,
				LocalKeyConfig: &config.LocalKeyConfig{
					Key: "secret-key",
					KeySettingsConfig: &config.KeySettingsConfig{
						AllowedIps:    allowedIps,
						Methods:       methods,
						AuthContracts: contracts,
					},
				},
			},
		},
	}

	p, err := auth.NewAuthProcessor(appCfg, nil)
	if err != nil {
		t.Fatalf("NewAuthProcessor error: %v", err)
	}
	return p
}

// helper to create HttpAuthPayload with optional headers
func newPayload(t *testing.T, headers map[string]string) *auth.HttpAuthPayload {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return auth.NewHttpAuthPayload(req)
}

// -------------------- Authenticate delegation tests --------------------

func TestBasicAuthProcessor_Authenticate_TokenSuccess(t *testing.T) {
	// token strategy expects this value
	processor := newBasicProcessor(t, "tok-123", nil, nil, nil)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreToken: "tok-123",
	})

	err := processor.Authenticate(context.Background(), payload)
	assert.NoError(t, err)
}

func TestBasicAuthProcessor_Authenticate_TokenInvalid(t *testing.T) {
	processor := newBasicProcessor(t, "tok-123", nil, nil, nil)

	// wrong token
	payload := newPayload(t, map[string]string{
		auth.XNodecoreToken: "bad-token",
	})

	err := processor.Authenticate(context.Background(), payload)
	assert.ErrorContains(t, err, "invalid secret token")
}

// -------------------- PreKeyValidate tests --------------------

func TestBasicAuthProcessor_PreKeyValidate_Success(t *testing.T) {
	// Allowed IP list contains 10.0.0.1
	processor := newBasicProcessor(t, "tok-123", []string{"10.0.0.1"}, nil, nil)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "secret-key",
		auth.XNodecoreToken: "tok-123",
	})

	// context with XFF = 10.0.0.1
	ctx := test_utils.CtxWithXFF("10.0.0.1")

	_, err := processor.PreKeyValidate(ctx, payload)
	assert.NoError(t, err)
}

func TestBasicAuthProcessor_PreKeyValidate_MissingHeader_Error(t *testing.T) {
	processor := newBasicProcessor(t, "tok-123", []string{"10.0.0.1"}, nil, nil)

	// no X-Nodecore-Key header provided
	payload := newPayload(t, map[string]string{auth.XNodecoreToken: "tok-123"})

	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.ErrorContains(t, err, "api-key must be provided")
}

func TestBasicAuthProcessor_PreKeyValidate_KeyNotFound_Error(t *testing.T) {
	processor := newBasicProcessor(t, "tok-123", []string{"10.0.0.1"}, nil, nil)

	// set a non-existing key value
	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "unknown-key",
		auth.XNodecoreToken: "tok-123",
	})

	_, err := processor.PreKeyValidate(context.Background(), payload)
	assert.ErrorContains(t, err, "specified api-key not found")
}

func TestBasicAuthProcessor_PreKeyValidate_IPNotAllowed_Error(t *testing.T) {
	processor := newBasicProcessor(t, "tok-123", []string{"192.168.0.10"}, nil, nil)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "secret-key",
		auth.XNodecoreToken: "tok-123",
	})

	// context IP does not match allowed list
	ctx := test_utils.CtxWithXFF("8.8.8.8")

	_, err := processor.PreKeyValidate(ctx, payload)
	assert.ErrorContains(t, err, "ips [8.8.8.8] are not allowed")
}

// -------------------- PostKeyValidate tests --------------------

func TestBasicAuthProcessor_PostKeyValidate_Success(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	processor := newBasicProcessor(t, "tok-123", []string{"127.0.0.1"}, methods, contracts)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "secret-key",
		auth.XNodecoreToken: "tok-123",
	})

	// Build a real RequestHolder for eth_call with 'to'
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})

	err := processor.PostKeyValidate(context.Background(), payload, req)
	assert.NoError(t, err)
}

func TestBasicAuthProcessor_PostKeyValidate_MethodNotAllowed_Error(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_getLogs"}} // not allowing eth_call
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	processor := newBasicProcessor(t, "tok-123", []string{"127.0.0.1"}, methods, contracts)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "secret-key",
		auth.XNodecoreToken: "tok-123",
	})

	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})

	err := processor.PostKeyValidate(context.Background(), payload, req)
	assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
}

func TestBasicAuthProcessor_PostKeyValidate_ContractNotAllowed_Error(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xdef"}} // not allowing 0xabc
	processor := newBasicProcessor(t, "tok-123", []string{"127.0.0.1"}, methods, contracts)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "secret-key",
		auth.XNodecoreToken: "tok-123",
	})

	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})

	err := processor.PostKeyValidate(context.Background(), payload, req)
	assert.ErrorContains(t, err, "'0xabc' address is not allowed")
}

func TestBasicAuthProcessor_PostKeyValidate_MissingHeader_Error(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	processor := newBasicProcessor(t, "tok-123", []string{"127.0.0.1"}, methods, contracts)

	payload := newPayload(t, map[string]string{
		// no X-Nodecore-Key
		auth.XNodecoreToken: "tok-123",
	})

	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})

	err := processor.PostKeyValidate(context.Background(), payload, req)
	assert.ErrorContains(t, err, "api-key must be provided")
}

func TestBasicAuthProcessor_PostKeyValidate_KeyNotFound_Error(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	processor := newBasicProcessor(t, "tok-123", []string{"127.0.0.1"}, methods, contracts)

	payload := newPayload(t, map[string]string{
		auth.XNodecoreKey:   "unknown-key",
		auth.XNodecoreToken: "tok-123",
	})

	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})

	err := processor.PostKeyValidate(context.Background(), payload, req)
	assert.ErrorContains(t, err, "specified api-key not found")
}
