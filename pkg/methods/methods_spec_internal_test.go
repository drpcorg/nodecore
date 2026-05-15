package specs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnrichSpecsPopulatesConnectorMethods(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"rpc": newInternalTestMethodSpec("rpc", []string{"json-rpc", "websocket"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"ws": newInternalTestMethodSpec("ws", []string{"websocket"}, nil,
			newInternalTestMethod("eth_subscribe", "common", true, false),
		),
		"bundle": newInternalTestMethodSpec("bundle", nil, []string{"rpc", "ws"}),
	}

	err := enrichSpecs(specs)
	require.NoError(t, err)

	bundle := resolvedSpecs["bundle"]
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.connectors)
	require.Len(t, bundle.connectors.byConnector, 2)

	jsonRpcMethods := bundle.connectors.byConnector[JsonRpcConnector]
	require.NotNil(t, jsonRpcMethods)
	assert.Contains(t, jsonRpcMethods.defaultMethods(), "eth_call")
	assert.NotContains(t, jsonRpcMethods.defaultMethods(), "eth_subscribe")

	websocketMethods := bundle.connectors.byConnector[WebsocketConnector]
	require.NotNil(t, websocketMethods)
	assert.Contains(t, websocketMethods.defaultMethods(), "eth_call")
	assert.Contains(t, websocketMethods.defaultMethods(), "eth_subscribe")
}

func TestEnrichSpecsAppliesBundleOverridesAcrossConnectorMethods(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"base": newInternalTestMethodSpec("base", []string{"json-rpc", "websocket"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
			newInternalTestMethod("eth_chainId", "common", true, true),
		),
		"bundle": newInternalTestMethodSpec("bundle", nil, []string{"base"},
			newInternalTestMethod("eth_call", "common", false, true),
		),
	}

	err := enrichSpecs(specs)
	require.NoError(t, err)

	bundle := resolvedSpecs["bundle"]
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.connectors)
	require.Len(t, bundle.connectors.byConnector, 2)

	assert.NotContains(t, bundle.connectors.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
	assert.Contains(t, bundle.connectors.byConnector[JsonRpcConnector].defaultMethods(), "eth_chainId")
	assert.NotContains(t, bundle.connectors.byConnector[WebsocketConnector].defaultMethods(), "eth_call")
	assert.Contains(t, bundle.connectors.byConnector[WebsocketConnector].defaultMethods(), "eth_chainId")
}

func TestEnrichSpecsDoesNotMutateImportedPlainSpecMethods(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"left": newInternalTestMethodSpec("left", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"right": newInternalTestMethodSpec("right", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_chainId", "common", true, true),
		),
		"parent": newInternalTestMethodSpec("parent", []string{"json-rpc"}, []string{"left", "right"}),
	}

	err := enrichSpecs(specs)
	require.NoError(t, err)

	left := resolvedSpecs["left"]
	require.NotNil(t, left)
	require.NotNil(t, left.methods)
	assert.Contains(t, left.methods.defaultMethods(), "eth_call")
	assert.NotContains(t, left.methods.defaultMethods(), "eth_chainId")
	assert.Contains(t, left.methods.byGroup["common"], "eth_call")
	assert.NotContains(t, left.methods.byGroup["common"], "eth_chainId")

	parent := resolvedSpecs["parent"]
	require.NotNil(t, parent)
	require.NotNil(t, parent.methods)
	assert.Contains(t, parent.methods.defaultMethods(), "eth_call")
	assert.Contains(t, parent.methods.defaultMethods(), "eth_chainId")
}

func newInternalTestMethodSpec(name string, apiConnectors []string, imports []string, methods ...*MethodData) *MethodSpec {
	specData := &SpecData{
		Name:          name,
		ApiConnectors: apiConnectors,
		apiConnectors: make([]ApiConnectorType, 0, len(apiConnectors)),
	}
	for _, connectorName := range apiConnectors {
		specData.apiConnectors = append(specData.apiConnectors, GetApiConnectorType(connectorName))
	}

	return &MethodSpec{
		SpecData:    specData,
		SpecImports: imports,
		Methods:     methods,
	}
}

func newInternalTestMethod(name, group string, enabled bool, cacheable bool) *MethodData {
	return &MethodData{
		Name:    name,
		Group:   group,
		Enabled: new(enabled),
		Settings: &MethodSettings{
			Cacheable: new(cacheable),
		},
	}
}
