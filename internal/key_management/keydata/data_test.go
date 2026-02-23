package keydata_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

func TestCheckMethod_AllowedContainsMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call", "eth_getLogs"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_getLogs")
	assert.NoError(t, err)
}

func TestCheckMethod_AllowedDoesNotContainMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_getBalance")
	assert.ErrorContains(t, err, "method 'eth_getBalance' is not allowed")
}

func TestCheckMethod_ForbiddenContainsMethod_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_syncing")
	assert.ErrorContains(t, err, "method 'eth_syncing' is not allowed")
}

func TestCheckMethod_ForbiddenDoesNotContainMethod_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Forbidden: []string{"eth_syncing"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodOnlyInAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_syncing"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.NoError(t, err)
}

func TestCheckMethod_BothLists_MethodInAllowedAndForbidden_ReturnsError(t *testing.T) {
	cfg := &config.AuthMethods{Allowed: []string{"eth_call"}, Forbidden: []string{"eth_call"}}
	err := keydata.CheckMethod(cfg.Allowed, cfg.Forbidden, "eth_call")
	assert.ErrorContains(t, err, "method 'eth_call' is not allowed")
}

// -------------------- Tests for CheckContracts (eth_call) --------------------

func TestCheckContracts_NilOrEmptyAllowed_ReturnsNil(t *testing.T) {
	// nil config
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xabc"}, "latest"})
	err := keydata.CheckContracts(nil, req)
	assert.NoError(t, err)

	// empty allowed list
	cfg := &config.AuthContracts{Allowed: []string{}}
	err = keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthCall_MissingToParam_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// params[0] exists but no 'to'
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{}, "latest"})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'to' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthCall_ToParamNotString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": 123}, "latest"})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'to' param must be string")
}

func TestCheckContracts_EthCall_ToParamNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xallowed"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xnot"}, "latest"})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthCall_ToParamAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc", "0xdef"}}
	req := test_utils.NewUpstreamRequest(t, "eth_call", []any{map[string]any{"to": "0xdef"}, "latest"})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

// -------------------- Tests for CheckContracts (eth_getLogs) --------------------

func TestCheckContracts_EthGetLogs_MissingAddress_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'address' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthGetLogs_AddressStringAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xabc"}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressStringNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": "0xnot"}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0xnot' address is not allowed")
}

func TestCheckContracts_EthGetLogs_AddressArrayAllAllowed_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x2"}}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}

func TestCheckContracts_EthGetLogs_AddressArrayWithNonString_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", 2}}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "value in 'address' param must be string")
}

func TestCheckContracts_EthGetLogs_AddressArrayOneNotAllowed_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0x1", "0x2"}}
	req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": []any{"0x1", "0x3"}}})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.ErrorContains(t, err, "'0x3' address is not allowed")
}

// -------------------- Tests for CheckContracts with other method --------------------

func TestCheckContracts_AnotherMethod_Ignored_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// Method not checked by CheckContracts => should return nil regardless of body
	req := test_utils.NewUpstreamRequest(t, "eth_blockNumber", []any{})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}
