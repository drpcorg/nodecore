package auth_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

// -------------------- Tests for CheckMethod --------------------

func TestCheckMethod_NilConfig_ReturnsNil(t *testing.T) {
	err := auth.CheckMethod(nil, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_AllowedContainsMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call", "eth_getLogs"}}
	err := auth.CheckMethod(cfg, "eth_getLogs")
	assert.NoError(t, err)
}

func TestCheckMethod_AllowedDoesNotContainMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}}
	err := auth.CheckMethod(cfg, "eth_getBalance")
	assert.ErrorContains(t, err, "method 'eth_getBalance' is not allowed")
}

func TestCheckMethod_ForbiddenContainsMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := auth.CheckMethod(cfg, "eth_syncing")
	assert.ErrorContains(t, err, "method 'eth_syncing' is not allowed")
}

func TestCheckMethod_ForbiddenDoesNotContainMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := auth.CheckMethod(cfg, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodOnlyInAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_syncing"}}
	err := auth.CheckMethod(cfg, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodInAllowedAndForbidden_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_call"}}
	err := auth.CheckMethod(cfg, "eth_call")
	assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
}

// -------------------- Tests for CheckContracts (eth_call) --------------------

func TestCheckContracts_NilOrEmptyAllowed_ReturnsNil(t *testing.T) {
	// nil config
	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := auth.CheckContracts(nil, req)
	assert.NoError(t, err)

	// empty allowed list
	cfg := &config.AuthContracts{Allowed: []string{}}
	err = auth.CheckContracts(cfg, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthCall_MissingToParam_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// params[0] exists but no 'to'
	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{}, "latest"})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'to' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthCall_ToParamNotString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": 123}, "latest"})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'to' param must be string")
}

func TestCheckContracts_EthCall_ToParamNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xallowed"}}
	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xnot"}, "latest"})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthCall_ToParamAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc", "0xdef"}}
	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xdef"}, "latest"})
	err := auth.CheckContracts(cfg, req)
	assert.NoError(t, err)
}

// -------------------- Tests for CheckContracts (eth_getLogs) --------------------

func TestCheckContracts_EthGetLogs_MissingAddress_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{}})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'address' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthGetLogs_AddressStringAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xabc"}})
	err := auth.CheckContracts(cfg, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressStringNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xnot"}})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthGetLogs_AddressArrayAllAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x2"}}})
	err := auth.CheckContracts(cfg, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressArrayWithNonString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", 2}}})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "value in 'address' param must be string")
}

func TestCheckContracts_EthGetLogs_AddressArrayOneNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := newUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x3"}}})
	err := auth.CheckContracts(cfg, req)
	assert.ErrorContains(t, err, "'0x3' address is not allowed")
}

// -------------------- Tests for CheckContracts with other method --------------------

func TestCheckContracts_AnotherMethod_Ignored_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// Method not checked by CheckContracts => should return nil regardless of body
	req := newUpstreamRequest(t, "eth_blockNumber", []any{})
	err := auth.CheckContracts(cfg, req)
	assert.NoError(t, err)
}

// -------------------- Tests for LocalKey and KeyResolver --------------------

// helper to build a Local KeyConfig
func buildLocalKeyConfig(id, key string, allowedIps []string, methods *config.AuthMethods, contracts *config.AuthContracts) *config.KeyConfig {
	return &config.KeyConfig{
		Id:   id,
		Type: config.Local,
		LocalKeyConfig: &config.LocalKeyConfig{
			Key: key,
			KeySettingsConfig: &config.KeySettingsConfig{
				AllowedIps:    allowedIps,
				Methods:       methods,
				AuthContracts: contracts,
			},
		},
	}
}

func TestLocalKey_Id(t *testing.T) {
	cfg := buildLocalKeyConfig("key-id-1", "secret-1", []string{"127.0.0.1"}, nil, nil)
	lk := auth.NewLocalKey(cfg)
	assert.Equal(t, "key-id-1", lk.Id())
}

func TestNewLocalKey_AndKeyResolver_Retrieval(t *testing.T) {
	cfg := buildLocalKeyConfig("kid2", "secret-abc", []string{"127.0.0.1"}, nil, nil)
	resolver := auth.NewKeyResolver([]*config.KeyConfig{cfg})

	k, ok := resolver.GetKey("secret-abc")
	assert.True(t, ok, "expected key to be found by resolver")
	assert.Equal(t, "kid2", k.Id())

	_, ok = resolver.GetKey("unknown-secret")
	assert.False(t, ok, "expected not found for unknown key")
}

func TestLocalKey_PreCheckSetting_Allows_WithXFF(t *testing.T) {
	cfg := buildLocalKeyConfig("kid3", "secret-2", []string{"10.0.0.1"}, nil, nil)
	lk := auth.NewLocalKey(cfg)

	ctx := test_utils.CtxWithXFF("10.0.0.1")
	_, err := lk.PreCheckSetting(ctx)
	assert.NoError(t, err)
}

func TestLocalKey_PreCheckSetting_Allows_WithRemoteAddr_NoXFF(t *testing.T) {
	cfg := buildLocalKeyConfig("kid4", "secret-3", []string{"192.168.1.2"}, nil, nil)
	lk := auth.NewLocalKey(cfg)

	ctx := test_utils.CtxWithRemoteAddr("192.168.1.2:5555")
	_, err := lk.PreCheckSetting(ctx)
	assert.NoError(t, err)
}

func TestLocalKey_PreCheckSetting_Error_WhenNotAllowed_SingleIP(t *testing.T) {
	cfg := buildLocalKeyConfig("kid5", "secret-4", []string{"1.2.3.4"}, nil, nil)
	lk := auth.NewLocalKey(cfg)

	// use single IP in XFF so error message order is deterministic
	ctx := test_utils.CtxWithXFF("8.8.8.8")
	_, err := lk.PreCheckSetting(ctx)
	assert.ErrorContains(t, err, "ips [8.8.8.8] are not allowed")
}

func TestLocalKey_PostCheckSetting_Success(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	cfg := buildLocalKeyConfig("kid7", "secret-6", []string{"127.0.0.1"}, methods, contracts)
	lk := auth.NewLocalKey(cfg)

	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := lk.PostCheckSetting(context.Background(), req)
	assert.NoError(t, err)
}

func TestLocalKey_PostCheckSetting_MethodNotAllowed(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_getLogs"}} // not allowing eth_call
	contracts := &config.AuthContracts{Allowed: []string{"0xabc"}}
	cfg := buildLocalKeyConfig("kid8", "secret-7", []string{"127.0.0.1"}, methods, contracts)
	lk := auth.NewLocalKey(cfg)

	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := lk.PostCheckSetting(context.Background(), req)
	assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
}

func TestLocalKey_PostCheckSetting_ContractNotAllowed(t *testing.T) {
	methods := &config.AuthMethods{Allowed: []string{"eth_call"}}
	contracts := &config.AuthContracts{Allowed: []string{"0xdef"}} // not allowing 0xabc
	cfg := buildLocalKeyConfig("kid9", "secret-8", []string{"127.0.0.1"}, methods, contracts)
	lk := auth.NewLocalKey(cfg)

	req := newUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := lk.PostCheckSetting(context.Background(), req)
	assert.ErrorContains(t, err, "'0xabc' address is not allowed")
}
