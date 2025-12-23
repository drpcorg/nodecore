package keymanagement_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	keymanagement "github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

func TestLocalKey_Id(t *testing.T) {
	tests := []struct {
		name     string
		key      keymanagement.Key
		expected string
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("key-id-1", "secret-1", []string{"127.0.0.1"}, nil, nil),
			),
			expected: "key-id-1",
		},
		{
			name:     "drpc key",
			key:      &drpc.DrpcKey{KeyId: "drpc-key-id"},
			expected: "drpc-key-id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(t, test.expected, test.key.Id())
		})
	}
}

func TestLocalKey_PreCheckSetting_Allows_WithXFF(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid3", "secret-2", []string{"10.0.0.1"}, nil, nil),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", IpWhitelist: []string{"10.0.0.1"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			_, err := test.key.PreCheckSetting(test_utils.CtxWithXFF("10.0.0.1"))
			assert.NoError(t, err)
		})
	}
}

func TestLocalKey_PreCheckSetting_Allows_WithRemoteAddr_NoXFF(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid3", "secret-2", []string{"192.168.1.2"}, nil, nil),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", IpWhitelist: []string{"192.168.1.2"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			_, err := test.key.PreCheckSetting(test_utils.CtxWithRemoteAddr("192.168.1.2:5555"))
			assert.NoError(t, err)
		})
	}
}

func TestLocalKey_PreCheckSetting_Error_WhenNotAllowed_SingleIP(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid3", "secret-2", []string{"1.2.3.4"}, nil, nil),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", IpWhitelist: []string{"1.2.3.4"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			_, err := test.key.PreCheckSetting(test_utils.CtxWithXFF("8.8.8.8"))
			assert.ErrorContains(t, err, "ips [8.8.8.8] are not allowed")
		})
	}
}

func TestLocalKey_PostCheckSetting_Success(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid7", "secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_call"}}, &config.AuthContracts{Allowed: []string{"0xabc"}}),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", MethodsWhitelist: []string{"eth_call"}, ContractWhitelist: []string{"0xabc"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
			err := test.key.PostCheckSetting(context.Background(), req)
			assert.NoError(t, err)
		})
	}
}

func TestLocalKey_PostCheckSetting_MethodNotAllowed(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid7", "secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_getLogs"}}, &config.AuthContracts{Allowed: []string{"0xabc"}}),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", MethodsWhitelist: []string{"eth_getLogs"}, ContractWhitelist: []string{"0xabc"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
			err := test.key.PostCheckSetting(context.Background(), req)
			assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
		})
	}
}

func TestLocalKey_PostCheckSetting_ContractNotAllowed(t *testing.T) {
	tests := []struct {
		name string
		key  keymanagement.Key
	}{
		{
			name: "local key",
			key: keymanagement.NewLocalKey(
				test_utils.BuildLocalKeyConfig("kid7", "secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_call"}}, &config.AuthContracts{Allowed: []string{"0xdef"}}),
			),
		},
		{
			name: "drpc key",
			key:  &drpc.DrpcKey{KeyId: "drpc-key-id", MethodsWhitelist: []string{"eth_call"}, ContractWhitelist: []string{"0xdef"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
			err := test.key.PostCheckSetting(context.Background(), req)
			assert.ErrorContains(t, err, "'0xabc' address is not allowed")
		})
	}
}

func TestCheckMethod_AllowedContainsMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call", "eth_getLogs"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_getLogs")
	assert.NoError(t, err)
}

func TestCheckMethod_AllowedDoesNotContainMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_getBalance")
	assert.ErrorContains(t, err, "method 'eth_getBalance' is not allowed")
}

func TestCheckMethod_ForbiddenContainsMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_syncing")
	assert.ErrorContains(t, err, "method 'eth_syncing' is not allowed")
}

func TestCheckMethod_ForbiddenDoesNotContainMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodOnlyInAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_syncing"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodInAllowedAndForbidden_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_call"}}
	err := keymanagement.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
}

// -------------------- Tests for CheckContracts (eth_call) --------------------

func TestCheckContracts_NilOrEmptyAllowed_ReturnsNil(t *testing.T) {
	// nil config
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := keymanagement.CheckContracts(nil, req)
	assert.NoError(t, err)

	// empty allowed list
	cfg := &config.AuthContracts{Allowed: []string{}}
	err = keymanagement.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthCall_MissingToParam_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// params[0] exists but no 'to'
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{}, "latest"})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'to' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthCall_ToParamNotString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": 123}, "latest"})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'to' param must be string")
}

func TestCheckContracts_EthCall_ToParamNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xallowed"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xnot"}, "latest"})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthCall_ToParamAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc", "0xdef"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xdef"}, "latest"})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

// -------------------- Tests for CheckContracts (eth_getLogs) --------------------

func TestCheckContracts_EthGetLogs_MissingAddress_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'address' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthGetLogs_AddressStringAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xabc"}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressStringNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xnot"}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthGetLogs_AddressArrayAllAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x2"}}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressArrayWithNonString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", 2}}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "value in 'address' param must be string")
}

func TestCheckContracts_EthGetLogs_AddressArrayOneNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x3"}}})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0x3' address is not allowed")
}

// -------------------- Tests for CheckContracts with other method --------------------

func TestCheckContracts_AnotherMethod_Ignored_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// Method not checked by CheckContracts => should return nil regardless of body
	req := test_utils.NewUpstreamRequest(t, "eth_blockNumber", []any{})
	err := keymanagement.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}
