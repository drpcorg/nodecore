package specs_test

import (
	"os"
	"testing"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
)

func TestLoadSpecAndCheckGroupsAndDefaultParams(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/full")).Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethodsByConnectors("test", nil)

	defaultGroup, ok := spec[specs.DefaultMethodGroup]
	assert.True(t, ok)
	assert.Len(t, defaultGroup, 3)

	for _, method := range defaultGroup {
		assert.True(t, method.Enabled())
		assert.True(t, method.IsCacheable())
	}

	traceGroup, ok := spec["trace"]
	assert.True(t, ok)
	assert.Len(t, traceGroup, 2)
	for methodName := range traceGroup {
		_, ok = defaultGroup[methodName]
		assert.True(t, ok)
	}

	anotherGroup, ok := spec["super-group"]
	assert.True(t, ok)
	assert.Len(t, anotherGroup, 1)
	for methodName := range anotherGroup {
		_, ok = defaultGroup[methodName]
		assert.True(t, ok)
	}
}

func TestLoadSpecAndCheckCacheableAndEnabledParams(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/full")).Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethodsByConnectors("another_test", nil)

	defaultGroup, ok := spec[specs.DefaultMethodGroup]
	assert.True(t, ok)
	assert.Len(t, defaultGroup, 1)

	method1 := defaultGroup["test"]
	assert.False(t, method1.IsCacheable())
	assert.True(t, method1.Enabled())
}

func TestLoadSpecWithTheSameNameThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/same_names")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: spec with name 'test' already exists")
}

func TestLoadSpecEmptyDirThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty")).Load()

	assert.ErrorContains(t, err, "no method specs")
}

func TestLoadSpecEmptyNameThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_name")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: file - 'spec1.json', spec validation error: missing spec name")
}

func TestLoadSpecEmptySpecDataThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_spec_data")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: file - 'spec1.json', spec validation error: missing spec data")
}

func TestLoadSpecEmptyMethodNameThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_method_name")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty method name, file - 'spec1.json', index - 0")
}

func TestLoadSpecEmptyParserPathThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_parser_path")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'test' of 'spec1.json' validation, cause: empty tag-parser path")
}

func TestLoadSpecWrongParserReturnTypeThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/wrong_parser_return_type")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'test' of 'spec1.json' validation, cause: wrong return type of tag-parser - wrong")
}

func TestLoadSpecExistedMethodThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/existed_method")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: method 'test_another' already exists, file - 'spec1.json'")
}

func TestLoadSpecWrongJqPathThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/wrong_parser_path")).Load()

	assert.ErrorContains(t, err, "couldn't merge method specs: spec 'test', error 'couldn't parse a jq path of method test - unexpected token \"!\"'")
}

func TestLoadSpecWrongStickySettings(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/wrong_sticky")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'eth_uninstallFilter' of 'spec.json' validation, cause: both 'create-sticky' and 'send-sticky' are enabled")
}

func TestLoadSpecWrongDispatchSettings(t *testing.T) {
	tests := []struct {
		name        string
		dir         string
		expectedErr string
	}{
		{
			name:        "unknown dispatch",
			dir:         "test_specs/wrong_dispatch_unknown",
			expectedErr: "couldn't read method specs: error during method 'eth_sendRawTransaction' of 'spec.json' validation, cause: unknown dispatch policy - wrong",
		},
		{
			name:        "local conflict",
			dir:         "test_specs/wrong_dispatch_local",
			expectedErr: "couldn't read method specs: error during method 'eth_sendRawTransaction' of 'spec.json' validation, cause: dispatch cannot be used with local methods",
		},
		{
			name:        "subscription conflict",
			dir:         "test_specs/wrong_dispatch_subscription",
			expectedErr: "couldn't read method specs: error during method 'eth_subscribe' of 'spec.json' validation, cause: dispatch cannot be used with subscription methods",
		},
		{
			name:        "sticky conflict",
			dir:         "test_specs/wrong_dispatch_sticky",
			expectedErr: "couldn't read method specs: error during method 'eth_getFilterChanges' of 'spec.json' validation, cause: dispatch cannot be used with sticky methods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := specs.NewMethodSpecLoaderWithFs(os.DirFS(tt.dir)).Load()

			assert.ErrorContains(t, err, tt.expectedErr)
		})
	}
}

func TestLoadSpecMergeMethods(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/merge_methods")).Load()

	assert.NoError(t, err)

	spec1 := specs.GetSpecMethodsByConnectors("another", nil)
	assert.Equal(t, 5, len(spec1))

	spec2 := specs.GetSpecMethodsByConnectors("test", nil)
	assert.Equal(t, 6, len(spec2))

	assert.Equal(t, spec1["trace"]["test"], spec2["trace"]["test"])

	_, ok1 := spec1["common"]["call"]
	assert.True(t, ok1)
	_, ok2 := spec2["common"]["call"]
	assert.False(t, ok2)

	method1 := spec1["common"]["call_1"]
	assert.False(t, method1.IsCacheable())
	method2 := spec2["common"]["call_1"]
	assert.True(t, method2.IsCacheable())

	_, ok := spec2["super"]["call_22"]
	assert.True(t, ok)

	method := spec2["superduper"]["my_method"]
	assert.False(t, method.IsCacheable())
}

func TestLoadSpecNestedImports(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/nested_imports")).Load()

	assert.NoError(t, err)

	bundle := specs.GetSpecMethodsByConnectors("bundle", nil)
	assert.Len(t, bundle[specs.DefaultMethodGroup], 3)
	assert.Contains(t, bundle["trace"], "trace_call")
	assert.Contains(t, bundle[specs.DefaultMethodGroup], "net_version")
	assert.Contains(t, bundle[specs.SubMethodGroup], "eth_subscribe")

	child := specs.GetSpecMethodsByConnectors("child", nil)
	assert.Len(t, child["trace"], 1)
	assert.Contains(t, child["trace"], "child_trace")
	assert.NotContains(t, child["trace"], "trace_call")
	assert.Contains(t, child[specs.DefaultMethodGroup], "net_version")
	assert.Contains(t, child[specs.SubMethodGroup], "eth_subscribe")
}

func TestNetworkSpecsDisableUnsupportedGetProof(t *testing.T) {
	err := specs.NewMethodSpecLoader().Load()
	assert.NoError(t, err)

	for _, specName := range []string{"viction", "hyperliquid"} {
		t.Run(specName, func(t *testing.T) {
			assert.Nil(t, specs.GetSpecMethod(specName, "eth_getProof"))

			jsonRPCMethods := specs.GetSpecMethodsByConnectors(specName, []specs.ApiConnectorType{specs.JsonRpcConnector})
			assert.NotContains(t, jsonRPCMethods[specs.DefaultMethodGroup], "eth_getProof")
		})
	}

	restAdditionalMethods := specs.GetSpecMethodsByConnectors("hyperliquid", []specs.ApiConnectorType{specs.RestAdditional})
	assert.NotContains(t, restAdditionalMethods[specs.DefaultMethodGroup], "eth_getProof")
}

func TestAptosSpecLoadsAndMatchesRestRoutes(t *testing.T) {
	loader := specs.NewMethodSpecLoader()
	err := loader.Load()
	assert.NoError(t, err)

	template, params, ok := specs.MatchRestMethod("aptos", "GET#/v1/blocks/by_height/12345")
	assert.True(t, ok)
	assert.Equal(t, "GET#/v1/blocks/by_height/*", template)
	assert.Equal(t, []string{"12345"}, params)
}

func TestBitcoinSpecLoads(t *testing.T) {
	err := specs.NewMethodSpecLoader().Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethod("bitcoin", "getblock")
	assert.NotNil(t, spec)

	spec = specs.GetSpecMethod("bitcoin", "sendrawtransaction")
	assert.NotNil(t, spec)

	spec = specs.GetSpecMethod("bitcoin", "listunspent")
	assert.NotNil(t, spec)

	spec = specs.GetSpecMethod("bitcoin", "eth_call")
	assert.Nil(t, spec)

	template, params, ok := specs.MatchRestMethod("bitcoin", "GET#/address/bc1qxyz/utxo")
	assert.True(t, ok)
	assert.Equal(t, "GET#/address/*/utxo", template)
	assert.Equal(t, []string{"bc1qxyz"}, params)
}

func TestAptosSpecMatchesNestedAccountAndTableRoutes(t *testing.T) {
	loader := specs.NewMethodSpecLoader()
	err := loader.Load()
	assert.NoError(t, err)

	// wildcards span exactly one path segment, so every nested fullnode route
	// needs its own template
	for method, want := range map[string]string{
		"GET#/v1/info":                                     "GET#/v1/info",
		"GET#/v1/accounts/0xabc/transactions":              "GET#/v1/accounts/*/transactions",
		"GET#/v1/accounts/0xabc/transaction_summaries":     "GET#/v1/accounts/*/transaction_summaries",
		"GET#/v1/transactions/auxiliary_info":              "GET#/v1/transactions/auxiliary_info",
		"GET#/v1/accounts/0xabc/events/2":                  "GET#/v1/accounts/*/events/*",
		"GET#/v1/accounts/0xabc/events/0x1::m::Events/key": "GET#/v1/accounts/*/events/*/*",
		"GET#/v1/accounts/0xabc/balance/0x1::coin::Coin":   "GET#/v1/accounts/*/balance/*",
		"GET#/v1/transactions/wait_by_hash/0xhash":         "GET#/v1/transactions/wait_by_hash/*",
		"POST#/v1/transactions/encode_submission":          "POST#/v1/transactions/encode_submission",
		"POST#/v1/tables/0xhandle/raw_item":                "POST#/v1/tables/*/raw_item",
	} {
		template, _, ok := specs.MatchRestMethod("aptos", method)
		assert.True(t, ok, method)
		assert.Equal(t, want, template, method)
	}
}
