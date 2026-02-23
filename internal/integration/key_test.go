package integration_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration/drpc"
	"github.com/drpcorg/nodecore/internal/integration/local"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

func TestKey_Id(t *testing.T) {
	tests := []struct {
		name     string
		key      keydata.Key
		expected string
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"key-id-1",
				test_utils.BuildLocalKeyConfig("secret-1", []string{"127.0.0.1"}, nil, nil),
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

func TestKey_PreCheckSetting_Allows_WithXFF(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid3",
				test_utils.BuildLocalKeyConfig("secret-2", []string{"10.0.0.1"}, nil, nil),
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

func TestKey_PreCheckSetting_Allows_WithRemoteAddr_NoXFF(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid3",
				test_utils.BuildLocalKeyConfig("secret-2", []string{"192.168.1.2"}, nil, nil),
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

func TestKey_PreCheckSetting_Error_WhenNotAllowed_SingleIP(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid3",
				test_utils.BuildLocalKeyConfig("secret-2", []string{"1.2.3.4"}, nil, nil),
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

func TestKey_PostCheckSetting_Success(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid7",
				test_utils.BuildLocalKeyConfig("secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_call"}}, &config.AuthContracts{Allowed: []string{"0xabc"}}),
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

func TestKey_PostCheckSetting_MethodNotAllowed(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid7",
				test_utils.BuildLocalKeyConfig("secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_getLogs"}}, &config.AuthContracts{Allowed: []string{"0xabc"}}),
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

func TestKey_PostCheckSetting_ContractNotAllowed(t *testing.T) {
	tests := []struct {
		name string
		key  keydata.Key
	}{
		{
			name: "local key",
			key: local.NewLocalKey(
				"kid7",
				test_utils.BuildLocalKeyConfig("secret-6", []string{"127.0.0.1"}, &config.AuthMethods{Allowed: []string{"eth_call"}}, &config.AuthContracts{Allowed: []string{"0xdef"}}),
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
