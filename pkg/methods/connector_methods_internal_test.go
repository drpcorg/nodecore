package specs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConnectorMethodsInitializesEmptyMap(t *testing.T) {
	methods := newConnectorMethods()

	require.NotNil(t, methods)
	assert.Empty(t, methods.byConnector)
	assert.Empty(t, methods.apiConnectors)
}

func TestNewConnectorMethodsFromSpecBuildsDistinctConnectorBuckets(t *testing.T) {
	spec := newInternalTestMethodSpec("test", []string{"json-rpc", "websocket"}, nil,
		newInternalTestMethod("eth_call", "common", true, true),
		newMethodDataWithSubscription("eth_subscribe", "filters", true, true),
		newInternalTestMethod("debug_traceCall", "trace", false, true),
	)

	methods, err := newConnectorMethodsFromSpec(spec, []ApiConnectorType{JsonRpcConnector, WebsocketConnector}, true)
	require.NoError(t, err)

	require.Len(t, methods.byConnector, 2)

	jsonRPCMethods := methods.byConnector[JsonRpcConnector]
	websocketMethods := methods.byConnector[WebsocketConnector]
	require.NotNil(t, jsonRPCMethods)
	require.NotNil(t, websocketMethods)

	assert.Contains(t, jsonRPCMethods.defaultMethods(), "eth_call")
	assert.Contains(t, websocketMethods.defaultMethods(), "eth_call")
	assert.Contains(t, jsonRPCMethods.subMethods(), "eth_subscribe")
	assert.Contains(t, websocketMethods.subMethods(), "eth_subscribe")
	assert.NotContains(t, jsonRPCMethods.defaultMethods(), "debug_traceCall")
	assert.NotContains(t, websocketMethods.defaultMethods(), "debug_traceCall")

	assert.NotSame(t, jsonRPCMethods, websocketMethods)
	assert.NotSame(t, jsonRPCMethods.method("eth_call"), websocketMethods.method("eth_call"))
}

func TestNewConnectorMethodsFromSpecReturnsMethodGroupError(t *testing.T) {
	spec := &MethodSpec{
		Methods: []*MethodData{
			{
				Name:    "broken",
				Group:   "trace",
				Enabled: new(true),
				TagParser: &TagParser{
					ReturnType: StringType,
					Path:       "!",
				},
			},
		},
	}

	methods, err := newConnectorMethodsFromSpec(spec, []ApiConnectorType{JsonRpcConnector}, true)
	require.Error(t, err)
	assert.Nil(t, methods)
	assert.ErrorContains(t, err, "couldn't parse a jq path of method broken")
}

func TestConnectorMethodsMergeImportsSkipsMissingAndFiltersConnectors(t *testing.T) {
	current := newConnectorMethods()
	importedSpec := newResolvedSpec(
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

	importedSpecs := newImportedSpecData()
	importedSpecs.add("imported", importedSpec)
	importedSpecs.specsByName["empty"] = nil

	err := current.mergeImports(importedSpecs, []ApiConnectorType{JsonRpcConnector}, []string{"empty", "missing", "imported"})
	require.NoError(t, err)

	require.Len(t, current.byConnector, 1)
	assert.Contains(t, current.byConnector, JsonRpcConnector)
	assert.NotContains(t, current.byConnector, WebsocketConnector)
	assert.Contains(t, current.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
}

func TestConnectorMethodsMergeFromNilImportedMethods(t *testing.T) {
	current := newConnectorMethods()
	current.byConnector[JsonRpcConnector] = newMethodGroups()
	current.byConnector[JsonRpcConnector].add("trace", newMethodGroupTestMethod("eth_call", true, true))

	err := current.mergeFrom(nil, nil)
	require.NoError(t, err)

	require.Len(t, current.byConnector, 1)
	assert.Contains(t, current.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
}

func TestConnectorMethodsMergeFromAdoptsAllConnectorsWhenAllowedSetIsEmpty(t *testing.T) {
	current := newConnectorMethods()
	imported := newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
		JsonRpcConnector: {
			"eth_call": newMethodGroupTestMethod("eth_call", true, false),
		},
		WebsocketConnector: {
			"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
		},
	})

	err := current.mergeFrom(imported, nil)
	require.NoError(t, err)

	require.Len(t, current.byConnector, 2)
	assert.Contains(t, current.byConnector[JsonRpcConnector].defaultMethods(), "eth_call")
	assert.Contains(t, current.byConnector[WebsocketConnector].defaultMethods(), "eth_subscribe")
	assert.NotSame(
		t,
		imported.byConnector[JsonRpcConnector].method("eth_call"),
		current.byConnector[JsonRpcConnector].method("eth_call"),
	)

	current.byConnector[JsonRpcConnector].method("eth_call").Name = "mutated"
	assert.Equal(t, "eth_call", imported.byConnector[JsonRpcConnector].method("eth_call").Name)
}

func TestConnectorMethodsMergeFromHonorsAllowedConnectorSet(t *testing.T) {
	current := newConnectorMethods()
	imported := newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
		JsonRpcConnector: {
			"eth_call": newMethodGroupTestMethod("eth_call", true, true),
		},
		RestConnector: {
			"eth_getBalance": newMethodGroupTestMethod("eth_getBalance", true, true),
		},
	})

	err := current.mergeFrom(imported, map[ApiConnectorType]struct{}{
		RestConnector: {},
	})
	require.NoError(t, err)

	require.Len(t, current.byConnector, 1)
	assert.NotContains(t, current.byConnector, JsonRpcConnector)
	assert.Contains(t, current.byConnector[RestConnector].defaultMethods(), "eth_getBalance")
}

func TestConnectorMethodsCollectTypesAddsConnectorKeys(t *testing.T) {
	methods := newConnectorMethodsForTests(map[ApiConnectorType]map[string]*Method{
		JsonRpcConnector: {
			"eth_call": newMethodGroupTestMethod("eth_call", true, true),
		},
		WebsocketConnector: {
			"eth_subscribe": newMethodGroupTestMethod("eth_subscribe", true, true),
		},
	})

	connectorTypes := map[ApiConnectorType]struct{}{
		RestConnector: {},
	}
	methods.collectTypes(connectorTypes)

	assert.Len(t, connectorTypes, 3)
	assert.Contains(t, connectorTypes, RestConnector)
	assert.Contains(t, connectorTypes, JsonRpcConnector)
	assert.Contains(t, connectorTypes, WebsocketConnector)
}

func TestConnectorTypeSetDeduplicatesConnectorTypes(t *testing.T) {
	connectorTypes := connectorTypeSet([]ApiConnectorType{
		WebsocketConnector,
		JsonRpcConnector,
		WebsocketConnector,
	})

	assert.Len(t, connectorTypes, 2)
	assert.Contains(t, connectorTypes, JsonRpcConnector)
	assert.Contains(t, connectorTypes, WebsocketConnector)
}

func TestConnectorMethodsInitConnectorsSortsTypes(t *testing.T) {
	methods := &connectorMethods{
		byConnector: map[ApiConnectorType]*methodGroups{
			WebsocketConnector: newMethodGroups(),
			JsonRpcConnector:   newMethodGroups(),
			RestConnector:      newMethodGroups(),
		},
	}

	methods.initConnectors()

	assert.Equal(t, []ApiConnectorType{JsonRpcConnector, RestConnector, WebsocketConnector}, methods.apiConnectors)
}

func TestConnectorMethodsInitConnectorsLeavesEmptySetUntouched(t *testing.T) {
	methods := newConnectorMethods()

	methods.initConnectors()

	assert.Empty(t, methods.apiConnectors)
}

func newConnectorMethodsForTests(byConnector map[ApiConnectorType]map[string]*Method) *connectorMethods {
	methods := newConnectorMethods()
	for connectorType, byName := range byConnector {
		groups := newMethodGroups()
		for methodName, method := range byName {
			clonedMethod := cloneMethod(method)
			clonedMethod.Name = methodName
			groups.add("common", clonedMethod)
		}
		methods.byConnector[connectorType] = groups
	}

	return methods
}

func newMethodDataWithSubscription(name, group string, enabled bool, cacheable bool) *MethodData {
	return &MethodData{
		Name:    name,
		Group:   group,
		Enabled: new(enabled),
		Settings: &MethodSettings{
			Cacheable: new(cacheable),
			Subscription: &Subscription{
				IsSubscribe: true,
			},
		},
	}
}
