package specs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnrichSpecsSkipsAlreadyResolvedSpecs(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{
		"cached": newResolvedSpec(newMethodGroups(), newConnectorMethods()),
	}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	err := enrichSpecs(map[string]*MethodSpec{
		"cached": nil,
	})
	require.NoError(t, err)

	assert.Contains(t, resolvedSpecs, "cached")
}

func TestEnrichSpecReturnsNilWhenSpecAlreadyResolved(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{
		"cached": newResolvedSpec(newMethodGroups(), newConnectorMethods()),
	}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	err := enrichSpec("cached", map[string]*MethodSpec{}, map[string]bool{})
	require.NoError(t, err)
}

func TestEnrichSpecReturnsNotFoundError(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	err := enrichSpec("missing", map[string]*MethodSpec{}, map[string]bool{})
	require.Error(t, err)
	assert.EqualError(t, err, "spec 'missing' not found")
}

func TestEnrichSpecReturnsCircularImportError(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"loop": newInternalTestMethodSpec("loop", []string{"json-rpc"}, []string{"other"}),
	}

	err := enrichSpec("loop", specs, map[string]bool{"loop": true})
	require.Error(t, err)
	assert.EqualError(t, err, "spec 'loop', error 'circular spec import detected'")
}

func TestEnrichSpecLeafRemovesDisabledMethods(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"leaf": newInternalTestMethodSpec("leaf", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
			newInternalTestMethod("debug_traceCall", "trace", false, true),
		),
	}

	err := enrichSpec("leaf", specs, map[string]bool{})
	require.NoError(t, err)

	leaf := resolvedSpecs["leaf"]
	require.NotNil(t, leaf)
	require.NotNil(t, leaf.methods)
	require.NotNil(t, leaf.connectors)
	assert.Contains(t, leaf.methods.defaultMethods(), "eth_call")
	assert.NotContains(t, leaf.methods.defaultMethods(), "debug_traceCall")
	assert.Contains(t, leaf.connectors.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
	assert.NotContains(t, leaf.connectors.byConnector[JsonRpcConnector].defaultMethods(), "debug_traceCall")
	assert.Equal(t, []ApiConnectorType{JsonRpcConnector}, leaf.connectors.apiConnectors)
}

func TestEnrichSpecImportedDisabledOverrideRemovesInheritedMethod(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"base": newInternalTestMethodSpec("base", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
			newInternalTestMethod("eth_chainId", "common", true, true),
		),
		"child": newInternalTestMethodSpec("child", []string{"json-rpc"}, []string{"base"},
			newInternalTestMethod("eth_call", "common", false, true),
		),
	}

	err := enrichSpec("child", specs, map[string]bool{})
	require.NoError(t, err)

	child := resolvedSpecs["child"]
	require.NotNil(t, child)
	assert.NotContains(t, child.methods.defaultMethods(), "eth_call")
	assert.Contains(t, child.methods.defaultMethods(), "eth_chainId")
	assert.NotContains(t, child.connectors.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
	assert.Contains(t, child.connectors.byConnector[JsonRpcConnector].defaultMethods(), "eth_chainId")
}

func TestResolveImportedSpecsReturnsMissingImportError(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc"}, nil)
	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"missing"}, map[string]*MethodSpec{}, map[string]bool{}, newMethodGroups())
	require.Error(t, err)
	assert.Nil(t, importedSpecs)
	assert.EqualError(t, err, "imported spec missing not found")
}

func TestResolveImportedSpecsMergesMethodsAndCollectsConnectorTypes(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"rpc": newInternalTestMethodSpec("rpc", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"ws": newInternalTestMethodSpec("ws", []string{"websocket"}, nil,
			newMethodDataWithSubscription("eth_subscribe", "filters", true, true),
		),
	}

	currentMethods := newMethodGroups()
	currentSpec := newInternalTestMethodSpec("bundle", nil, []string{"rpc", "ws"})
	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"rpc", "ws"}, specs, map[string]bool{}, currentMethods)
	require.NoError(t, err)
	require.NotNil(t, importedSpecs)

	assert.Contains(t, currentMethods.defaultMethods(), "eth_call")
	assert.Contains(t, currentMethods.defaultMethods(), "eth_subscribe")
	assert.Contains(t, importedSpecs.specsByName, "rpc")
	assert.Contains(t, importedSpecs.specsByName, "ws")
	assert.Contains(t, importedSpecs.connectorTypes, JsonRpcConnector)
	assert.Contains(t, importedSpecs.connectorTypes, WebsocketConnector)
}

func TestResolveImportedSpecsRejectsSameLevelDuplicateMethods(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	specs := map[string]*MethodSpec{
		"left": newInternalTestMethodSpec("left", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"right": newInternalTestMethodSpec("right", []string{"websocket"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
	}

	currentSpec := newInternalTestMethodSpec("bundle", nil, []string{"left", "right"})
	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"left", "right"}, specs, map[string]bool{}, newMethodGroups())
	require.Error(t, err)
	assert.Nil(t, importedSpecs)
	assert.EqualError(t, err, "same-level imported specs left and right define method eth_call")
}

func TestResolveImportedSpecsRejectsPlainImportWithDifferentConnectors(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc"}, []string{"imported"})
	specs := map[string]*MethodSpec{
		"imported": newInternalTestMethodSpec("imported", []string{"websocket"}, nil,
			newInternalTestMethod("eth_subscribe", "common", true, true),
		),
	}

	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"imported"}, specs, map[string]bool{}, newMethodGroups())
	require.Error(t, err)
	assert.Nil(t, importedSpecs)
	assert.ErrorContains(t, err, "plain spec current cannot import spec imported because api connectors differ")
	assert.ErrorContains(t, err, "current=[json-rpc]")
	assert.ErrorContains(t, err, "imported=[websocket]")
}

func TestResolveImportedSpecsAllowsPlainImportWithSameConnectors(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc", "websocket"}, []string{"imported"})
	specs := map[string]*MethodSpec{
		"imported": newInternalTestMethodSpec("imported", []string{"websocket", "json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
	}

	currentMethods := newMethodGroups()
	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"imported"}, specs, map[string]bool{}, currentMethods)
	require.NoError(t, err)
	require.NotNil(t, importedSpecs)
	assert.Contains(t, currentMethods.defaultMethods(), "eth_call")
}

func TestResolveImportedSpecsAllowsBundleImportAcrossDifferentConnectors(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	currentSpec := newInternalTestMethodSpec("bundle", nil, []string{"rpc", "ws"})
	specs := map[string]*MethodSpec{
		"rpc": newInternalTestMethodSpec("rpc", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"ws": newInternalTestMethodSpec("ws", []string{"websocket"}, nil,
			newMethodDataWithSubscription("eth_subscribe", "common", true, true),
		),
	}

	currentMethods := newMethodGroups()
	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"rpc", "ws"}, specs, map[string]bool{}, currentMethods)
	require.NoError(t, err)
	require.NotNil(t, importedSpecs)
	assert.Contains(t, currentMethods.defaultMethods(), "eth_call")
	assert.Contains(t, currentMethods.defaultMethods(), "eth_subscribe")
}

func TestResolveImportedSpecsRejectsPlainImportOfBundleWithDifferentEffectiveConnectors(t *testing.T) {
	resolvedSpecs = map[string]*resolvedSpec{}
	t.Cleanup(func() {
		resolvedSpecs = nil
	})

	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc"}, []string{"bundle"})
	specs := map[string]*MethodSpec{
		"left": newInternalTestMethodSpec("left", []string{"json-rpc"}, nil,
			newInternalTestMethod("eth_call", "common", true, true),
		),
		"right": newInternalTestMethodSpec("right", []string{"websocket"}, nil,
			newMethodDataWithSubscription("eth_subscribe", "common", true, true),
		),
		"bundle": newInternalTestMethodSpec("bundle", nil, []string{"left", "right"}),
	}

	importedSpecs, err := resolveImportedSpecs(currentSpec, []string{"bundle"}, specs, map[string]bool{}, newMethodGroups())
	require.Error(t, err)
	assert.Nil(t, importedSpecs)
	assert.ErrorContains(t, err, "plain spec current cannot import spec bundle because api connectors differ")
	assert.ErrorContains(t, err, "current=[json-rpc]")
	assert.ErrorContains(t, err, "imported=[json-rpc websocket]")
}

func TestMergeImportedSpecMethodsIgnoresNilImportedSpec(t *testing.T) {
	currentMethods := newMethodGroups()
	currentMethods.add("common", newMethodGroupTestMethod("eth_call", true, true))

	err := mergeImportedSpecMethods(currentMethods, map[string]string{}, "imported", nil)
	require.NoError(t, err)

	assert.Contains(t, currentMethods.defaultMethods(), "eth_call")
}

func TestMergeImportedSpecMethodsMergesMethods(t *testing.T) {
	currentMethods := newMethodGroups()
	importedMethods := newMethodGroups()
	importedMethods.add("trace", newMethodGroupTestMethod("eth_call", true, true))

	err := mergeImportedSpecMethods(
		currentMethods,
		map[string]string{},
		"imported",
		newResolvedSpec(importedMethods, nil),
	)
	require.NoError(t, err)

	assert.Contains(t, currentMethods.defaultMethods(), "eth_call")
	assert.Contains(t, currentMethods.byGroup["trace"], "eth_call")
}

func TestResolveConnectorTypesPrefersCurrentTypes(t *testing.T) {
	resolvedConnectorTypes := resolveConnectorTypes(
		[]ApiConnectorType{JsonRpcConnector},
		map[ApiConnectorType]struct{}{WebsocketConnector: {}},
	)

	assert.Equal(t, []ApiConnectorType{JsonRpcConnector}, resolvedConnectorTypes)
}

func TestResolveConnectorTypesFallsBackToImportedTypes(t *testing.T) {
	resolvedConnectorTypes := resolveConnectorTypes(
		nil,
		map[ApiConnectorType]struct{}{
			JsonRpcConnector:   {},
			WebsocketConnector: {},
		},
	)

	assert.ElementsMatch(t, []ApiConnectorType{JsonRpcConnector, WebsocketConnector}, resolvedConnectorTypes)
}

func TestValidateImportedSpecCompatibilityAllowsBundleImporter(t *testing.T) {
	currentSpec := newInternalTestMethodSpec("bundle", nil, []string{"imported"})
	importedSpec := newInternalTestMethodSpec("imported", []string{"websocket"}, nil)

	err := validateImportedSpecCompatibility(currentSpec, importedSpec, nil)
	require.NoError(t, err)
}

func TestValidateImportedSpecCompatibilityAllowsSameEffectiveConnectors(t *testing.T) {
	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc", "websocket"}, []string{"bundle"})
	importedSpec := newInternalTestMethodSpec("bundle", nil, []string{"left", "right"})
	importedResolvedSpec := newResolvedSpec(
		nil,
		newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
			JsonRpcConnector: {
				"eth_call": newMethodGroupTestMethod("eth_call", true, true),
			},
			WebsocketConnector: {
				"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
			},
		}),
	)
	importedResolvedSpec.connectors.initConnectors()

	err := validateImportedSpecCompatibility(currentSpec, importedSpec, importedResolvedSpec)
	require.NoError(t, err)
}

func TestValidateImportedSpecCompatibilityRejectsDifferentEffectiveConnectors(t *testing.T) {
	currentSpec := newInternalTestMethodSpec("current", []string{"json-rpc"}, []string{"bundle"})
	importedSpec := newInternalTestMethodSpec("bundle", nil, []string{"left", "right"})
	importedResolvedSpec := newResolvedSpec(
		nil,
		newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
			JsonRpcConnector: {
				"eth_call": newMethodGroupTestMethod("eth_call", true, true),
			},
			WebsocketConnector: {
				"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
			},
		}),
	)
	importedResolvedSpec.connectors.initConnectors()

	err := validateImportedSpecCompatibility(currentSpec, importedSpec, importedResolvedSpec)
	require.Error(t, err)
	assert.ErrorContains(t, err, "plain spec current cannot import spec bundle because api connectors differ")
}

func TestEffectiveConnectorTypesUsesPlainSpecConnectors(t *testing.T) {
	importedSpec := newInternalTestMethodSpec("imported", []string{"websocket", "json-rpc"}, nil)

	connectorTypes := effectiveConnectorTypes(importedSpec, nil)

	assert.Equal(t, []ApiConnectorType{WebsocketConnector, JsonRpcConnector}, connectorTypes)
}

func TestEffectiveConnectorTypesUsesResolvedBundleConnectors(t *testing.T) {
	importedSpec := newInternalTestMethodSpec("bundle", nil, []string{"left", "right"})
	importedResolvedSpec := newResolvedSpec(
		nil,
		newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
			JsonRpcConnector: {
				"eth_call": newMethodGroupTestMethod("eth_call", true, true),
			},
			WebsocketConnector: {
				"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
			},
		}),
	)
	importedResolvedSpec.connectors.initConnectors()

	connectorTypes := effectiveConnectorTypes(importedSpec, importedResolvedSpec)

	assert.Equal(t, []ApiConnectorType{JsonRpcConnector, WebsocketConnector}, connectorTypes)
}

func TestSameConnectorTypesComparesSets(t *testing.T) {
	assert.True(t, sameConnectorTypes(
		[]ApiConnectorType{JsonRpcConnector, WebsocketConnector},
		[]ApiConnectorType{WebsocketConnector, JsonRpcConnector, WebsocketConnector},
	))
	assert.False(t, sameConnectorTypes(
		[]ApiConnectorType{JsonRpcConnector},
		[]ApiConnectorType{JsonRpcConnector, WebsocketConnector},
	))
}

func TestConnectorTypeNamesReturnsSortedNames(t *testing.T) {
	assert.Equal(t, []string{"json-rpc", "websocket"}, connectorTypeNames([]ApiConnectorType{
		WebsocketConnector,
		JsonRpcConnector,
	}))
}

func TestValidateSameLevelImportedMethodsTracksOwners(t *testing.T) {
	owners := map[string]string{}
	importedMethods := newMethodGroups()
	importedMethods.add("trace", newMethodGroupTestMethod("eth_call", true, true))
	importedMethods.add("filters", newMethodGroupTestMethod("eth_subscribe", true, true))

	err := validateSameLevelImportedMethods(owners, "rpc", importedMethods)
	require.NoError(t, err)

	assert.Equal(t, "rpc", owners["eth_call"])
	assert.Equal(t, "rpc", owners["eth_subscribe"])
}

func TestValidateSameLevelImportedMethodsReturnsDuplicateError(t *testing.T) {
	importedMethods := newMethodGroups()
	importedMethods.add("trace", newMethodGroupTestMethod("eth_call", true, true))

	err := validateSameLevelImportedMethods(map[string]string{
		"eth_call": "left",
	}, "right", importedMethods)
	require.Error(t, err)
	assert.EqualError(t, err, "same-level imported specs left and right define method eth_call")
}

func TestNewResolvedSpecStoresFields(t *testing.T) {
	methods := newMethodGroups()
	connectors := newConnectorMethods()

	spec := newResolvedSpec(methods, connectors)

	require.NotNil(t, spec)
	assert.Same(t, methods, spec.methods)
	assert.Same(t, connectors, spec.connectors)
}

func TestNewImportedSpecDataInitializesMaps(t *testing.T) {
	importedSpecData := newImportedSpecData()

	require.NotNil(t, importedSpecData)
	assert.Empty(t, importedSpecData.specsByName)
	assert.Empty(t, importedSpecData.connectorTypes)
}

func TestImportedSpecDataAddIgnoresNilSpec(t *testing.T) {
	importedSpecData := newImportedSpecData()

	importedSpecData.add("nil", nil)

	assert.Empty(t, importedSpecData.specsByName)
	assert.Empty(t, importedSpecData.connectorTypes)
}

func TestImportedSpecDataAddStoresSpecAndCollectsConnectorTypes(t *testing.T) {
	importedSpecData := newImportedSpecData()
	spec := newResolvedSpec(
		nil,
		newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
			JsonRpcConnector: {
				"eth_call": newMethodGroupTestMethod("eth_call", true, true),
			},
			WebsocketConnector: {
				"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
			},
		}),
	)

	importedSpecData.add("spec", spec)

	assert.Same(t, spec, importedSpecData.specsByName["spec"])
	assert.Contains(t, importedSpecData.connectorTypes, JsonRpcConnector)
	assert.Contains(t, importedSpecData.connectorTypes, WebsocketConnector)
}

func TestWrapSpecErrorFormatsError(t *testing.T) {
	err := wrapSpecError("bundle", assert.AnError)

	require.Error(t, err)
	assert.EqualError(t, err, "spec 'bundle', error 'assert.AnError general error for testing'")
}
