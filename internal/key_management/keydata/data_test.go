package keydata_test

import (
	"encoding/json"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

// requestWithRawParams builds a request the way the server does, from params
// kept exactly as the client wrote them. A body built from a Go map cannot
// carry a repeated key, and that is what these tests are about.
func requestWithRawParams(method, params string) protocol.RequestHolder {
	return protocol.NewUpstreamJsonRpcRequest(
		"1",
		protocol.JsonRpcRequestBody{Id: json.RawMessage(`1`), Method: method, Params: json.RawMessage(params)},
		false,
		chains.GetMethodSpecNameByChain(chains.ETHEREUM),
	)
}

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

// The check reads one 'to', the node may read another: params are forwarded as
// the client wrote them, and a node decoding with Go's encoding/json takes the
// last of repeated keys and matches their names case-insensitively.
func TestCheckContracts_EthCall_RepeatedToParam_ReturnsError(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "same key twice", params: `[{"to":"0xabc","to":"0xother"},"latest"]`},
		{name: "another case", params: `[{"to":"0xabc","TO":"0xother"},"latest"]`},
		{name: "another case first", params: `[{"To":"0xother","to":"0xabc"},"latest"]`},
		{name: "escaped key", params: `[{"to":"0xabc","\u0074o":"0xother"},"latest"]`},
		{name: "both allowed", params: `[{"to":"0xabc","to":"0xabc"},"latest"]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
			err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_call", test.params))
			assert.ErrorContains(t, err, "'to' param must not be repeated")
		})
	}
}

// A name the check does not read is not a 'to' for the check, whatever a node
// makes of it: the request has no 'to' and is turned away as such.
func TestCheckContracts_EthCall_ToParamInAnotherCaseOnly_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_call", `[{"TO":"0xabc"},"latest"]`))
	assert.ErrorContains(t, err, "'to' param is mandatory due to the contracts settings")
}

func TestCheckContracts_EthCall_RawParamsWithOneToParam_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_call", `[{"from":"0x1","to":"0xabc","data":"0x"},"latest"]`))
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

// An EVM node reads a null or empty 'address' as "every contract", so letting
// either through would hand a restricted key the whole chain's logs.
func TestCheckContracts_EthGetLogs_AddressMatchingEveryContract_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		address any
	}{
		{name: "null", address: nil},
		{name: "empty array", address: []any{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
			req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": test.address}})
			err := keydata.CheckContracts(cfg.Allowed, req)
			assert.ErrorContains(t, err, "'address' param must be a string or a non-empty array of strings")
		})
	}
}

func TestCheckContracts_EthGetLogs_AddressOfUnsupportedType_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		address any
	}{
		{name: "number", address: 123},
		{name: "object", address: map[string]any{}},
		{name: "bool", address: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
			req := test_utils.NewUpstreamRequest(t, "eth_getLogs", []any{map[string]any{"address": test.address}})
			err := keydata.CheckContracts(cfg.Allowed, req)
			assert.ErrorContains(t, err, "'address' param must be a string or a non-empty array of strings")
		})
	}
}

// A repeated 'address' is the same hole as a null one: the check passes on the
// first, an allowed contract, while a node decoding with Go's encoding/json
// answers for the last - every contract, or one that is not allowed.
func TestCheckContracts_EthGetLogs_RepeatedAddress_ReturnsError(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "null after an allowed one", params: `[{"address":"0xabc","address":null}]`},
		{name: "empty array in another case", params: `[{"address":"0xabc","Address":[]}]`},
		{name: "another contract after an allowed one", params: `[{"address":"0xabc","address":"0xother"}]`},
		{name: "another case first", params: `[{"ADDRESS":"0xother","address":"0xabc"}]`},
		{name: "escaped key", params: `[{"address":"0xabc","\u0061ddress":"0xother"}]`},
		{name: "key that only folds to the name", params: `[{"address":"0xabc","addre\u017f\u017f":"0xother"}]`},
		{name: "both allowed", params: `[{"address":"0xabc","address":["0xabc"]}]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
			err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_getLogs", test.params))
			assert.ErrorContains(t, err, "'address' param must not be repeated")
		})
	}
}

func TestCheckContracts_EthGetLogs_AddressInAnotherCaseOnly_ReturnsError(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_getLogs", `[{"Address":"0xabc"}]`))
	assert.ErrorContains(t, err, "'address' param is mandatory due to the contracts settings")
}

// Other filter fields, and an 'address' deeper in the params, are not a
// second 'address' of the filter.
func TestCheckContracts_EthGetLogs_RawParamsWithOneAddress_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	params := `[{"fromBlock":"0x1","address":"0xabc","topics":[["0x1"]],"extra":{"address":"0xother"}}]`
	err := keydata.CheckContracts(cfg.Allowed, requestWithRawParams("eth_getLogs", params))
	assert.NoError(t, err)
}

// -------------------- Tests for CheckContracts with other method --------------------

func TestCheckContracts_AnotherMethod_Ignored_ReturnsNil(t *testing.T) {
	cfg := &config.AuthContracts{Allowed: []string{"0xabc"}}
	// Method not checked by CheckContracts => should return nil regardless of body
	req := test_utils.NewUpstreamRequest(t, "eth_blockNumber", []any{})
	err := keydata.CheckContracts(cfg.Allowed, req)
	assert.NoError(t, err)
}
