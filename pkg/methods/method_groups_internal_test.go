package specs

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMethodGroupsInitializesSyntheticGroups(t *testing.T) {
	groups := newMethodGroups()

	require.NotNil(t, groups)
	assert.Contains(t, groups.byGroup, DefaultMethodGroup)
	assert.Contains(t, groups.byGroup, SubMethodGroup)
	assert.Empty(t, groups.byGroup[DefaultMethodGroup])
	assert.Empty(t, groups.byGroup[SubMethodGroup])
}

func TestNewMethodGroupsFromSpecRemovesDisabledMethodsWhenRequested(t *testing.T) {
	spec := &MethodSpec{
		Methods: []*MethodData{
			newMethodGroupTestMethodData("eth_call", "trace", true),
			newMethodGroupTestSubscribeMethodData("eth_subscribe", "filters", true),
			newMethodGroupTestMethodData("debug_traceCall", "trace", false),
		},
	}

	groups, err := newMethodGroupsFromSpec(spec, []ApiConnectorType{JsonRpcConnector}, true)
	require.NoError(t, err)

	require.Contains(t, groups.byGroup["trace"], "eth_call")
	assert.Contains(t, groups.defaultMethods(), "eth_call")
	assert.Contains(t, groups.byGroup["filters"], "eth_subscribe")
	assert.Contains(t, groups.subMethods(), "eth_subscribe")
	assert.NotContains(t, groups.byGroup["trace"], "debug_traceCall")
	assert.NotContains(t, groups.defaultMethods(), "debug_traceCall")
	assert.Nil(t, groups.method("debug_traceCall"))
}

func TestNewMethodGroupsFromSpecKeepsDisabledMethodsForImportOverrides(t *testing.T) {
	spec := &MethodSpec{
		Methods: []*MethodData{
			newMethodGroupTestMethodData("debug_traceCall", "trace", false),
		},
	}

	groups, err := newMethodGroupsFromSpec(spec, []ApiConnectorType{JsonRpcConnector}, false)
	require.NoError(t, err)

	method := groups.method("debug_traceCall")
	require.NotNil(t, method)
	assert.False(t, method.Enabled())
	assert.Contains(t, groups.byGroup["trace"], "debug_traceCall")
	assert.Contains(t, groups.defaultMethods(), "debug_traceCall")
}

func TestNewMethodGroupsFromSpecReturnsMethodConversionError(t *testing.T) {
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

	groups, err := newMethodGroupsFromSpec(spec, []ApiConnectorType{JsonRpcConnector}, true)
	require.Error(t, err)
	assert.Nil(t, groups)
	assert.ErrorContains(t, err, `couldn't parse a jq path of method broken`)
}

func TestMethodGroupsAddIndexesMethodsAcrossGroups(t *testing.T) {
	groups := newMethodGroups()
	callMethod := newMethodGroupTestMethod("eth_call", true, true)
	subscribeMethod := newMethodGroupTestMethod("eth_subscribe", true, true)
	subscribeMethod.Subscription = &Subscription{IsSubscribe: true}

	groups.add("trace", callMethod)
	groups.add("filters", subscribeMethod)

	assert.Same(t, callMethod, groups.byGroup["trace"]["eth_call"])
	assert.Same(t, callMethod, groups.defaultMethods()["eth_call"])
	assert.Same(t, callMethod, groups.method("eth_call"))
	assert.Same(t, subscribeMethod, groups.byGroup["filters"]["eth_subscribe"])
	assert.Same(t, subscribeMethod, groups.defaultMethods()["eth_subscribe"])
	assert.Same(t, subscribeMethod, groups.subMethods()["eth_subscribe"])
	assert.Nil(t, groups.method("missing_method"))
	assert.NotContains(t, groups.subMethods(), "eth_call")
}

func TestMethodGroupsEnsureGroupIsIdempotent(t *testing.T) {
	groups := newMethodGroups()

	groups.ensureGroup("trace")
	groups.byGroup["trace"]["eth_call"] = newMethodGroupTestMethod("eth_call", true, true)
	groups.ensureGroup("trace")

	assert.Len(t, groups.byGroup["trace"], 1)
	assert.Contains(t, groups.byGroup["trace"], "eth_call")
}

func TestMethodGroupsMergeFromNilImportedGroups(t *testing.T) {
	groups := newMethodGroups()
	groups.add("trace", newMethodGroupTestMethod("eth_call", true, true))

	err := groups.mergeFrom(nil)
	require.NoError(t, err)

	assert.Contains(t, groups.byGroup["trace"], "eth_call")
	assert.Contains(t, groups.defaultMethods(), "eth_call")
}

func TestMethodGroupsMergeFromClonesAdoptedGroups(t *testing.T) {
	current := newMethodGroups()
	imported := newMethodGroups()
	importedMethod := newMethodGroupTestMethod("eth_call", true, false)
	imported.add("trace", importedMethod)

	err := current.mergeFrom(imported)
	require.NoError(t, err)

	require.Contains(t, current.byGroup["trace"], "eth_call")
	currentMethod := current.byGroup["trace"]["eth_call"]
	require.NotNil(t, currentMethod)
	assert.NotSame(t, importedMethod, currentMethod)
	assert.False(t, currentMethod.IsCacheable())
	assert.Contains(t, current.defaultMethods(), "eth_call")

	currentMethod.Name = "mutated"
	assert.Equal(t, "eth_call", importedMethod.Name)
}

func TestMethodGroupsMergeFromDeletesInheritedMethodWhenLocalOverrideDisabled(t *testing.T) {
	current := newMethodGroups()
	localOverride := newMethodGroupTestMethod("eth_subscribe", false, true)
	localOverride.Subscription = &Subscription{IsSubscribe: true}
	current.add("trace", localOverride)

	imported := newMethodGroups()
	importedMethod := newMethodGroupTestMethod("eth_subscribe", true, true)
	importedMethod.Subscription = &Subscription{IsSubscribe: true}
	imported.add("trace", importedMethod)

	err := current.mergeFrom(imported)
	require.NoError(t, err)

	assert.NotContains(t, current.byGroup["trace"], "eth_subscribe")
	assert.NotContains(t, current.defaultMethods(), "eth_subscribe")
	assert.NotContains(t, current.subMethods(), "eth_subscribe")
}

func TestMethodGroupsMergeFromPreservesLocalBoolFieldsAndBackfillsMissingSettings(t *testing.T) {
	current := newMethodGroups()

	currentMethod, err := fromMethodData(&MethodData{
		Name:    "eth_call",
		Group:   "trace",
		Enabled: new(true),
		Settings: &MethodSettings{
			Cacheable: new(false),
		},
	}, nil)
	require.NoError(t, err)
	current.add("trace", currentMethod)

	imported := newMethodGroups()

	importedMethod, err := fromMethodData(&MethodData{
		Name:    "eth_call",
		Group:   "trace",
		Enabled: new(false),
		Settings: &MethodSettings{
			Cacheable: new(true),
			Subscription: &Subscription{
				IsSubscribe: true,
				Method:      "eth_subscription",
				UnsubMethod: "eth_unsubscribe",
			},
		},
	}, nil)
	require.NoError(t, err)
	imported.add("trace", importedMethod)

	err = current.mergeFrom(imported)
	require.NoError(t, err)

	mergedMethod := current.byGroup["trace"]["eth_call"]
	require.NotNil(t, mergedMethod)
	assert.True(t, mergedMethod.Enabled())
	assert.False(t, mergedMethod.IsCacheable())
	require.NotNil(t, mergedMethod.Subscription)
	assert.True(t, mergedMethod.Subscription.IsSubscribe)
	assert.Equal(t, "eth_unsubscribe", mergedMethod.Subscription.UnsubMethod)
	assert.Contains(t, current.subMethods(), "eth_call")
}

func TestCloneMethodMapReturnsDistinctMethods(t *testing.T) {
	source := map[string]*Method{
		"eth_call":    newMethodGroupTestMethod("eth_call", true, true),
		"eth_getLogs": nil,
		"eth_chainId": newMethodGroupTestMethod("eth_chainId", false, false),
	}

	cloned := cloneMethodMap(source)

	require.Len(t, cloned, len(source))
	assert.Nil(t, cloned["eth_getLogs"])
	require.NotNil(t, cloned["eth_call"])
	assert.NotSame(t, source["eth_call"], cloned["eth_call"])
	assert.Equal(t, source["eth_call"].Name, cloned["eth_call"].Name)

	cloned["eth_call"].Name = "mutated"
	assert.Equal(t, "eth_call", source["eth_call"].Name)
}

func TestCloneMethodReturnsNilForNilInput(t *testing.T) {
	assert.Nil(t, cloneMethod(nil))
}

func TestBoolTransformerPreservesDestinationBool(t *testing.T) {
	transformer := boolTransformer{}

	assert.Nil(t, transformer.Transformer(reflect.TypeOf("")))

	transform := transformer.Transformer(reflect.TypeOf(true))
	require.NotNil(t, transform)

	dstFalse := false
	err := transform(reflect.ValueOf(&dstFalse).Elem(), reflect.ValueOf(true))
	require.NoError(t, err)
	assert.False(t, dstFalse)

	dstTrue := true
	err = transform(reflect.ValueOf(&dstTrue).Elem(), reflect.ValueOf(false))
	require.NoError(t, err)
	assert.True(t, dstTrue)
}

func newMethodGroupTestMethod(name string, enabled, cacheable bool) *Method {
	return &Method{
		Name:      name,
		enabled:   enabled,
		cacheable: cacheable,
	}
}

func newMethodGroupTestMethodData(name, group string, enabled bool) *MethodData {
	return &MethodData{
		Name:    name,
		Group:   group,
		Enabled: new(enabled),
	}
}

func newMethodGroupTestSubscribeMethodData(name, group string, enabled bool) *MethodData {
	return &MethodData{
		Name:    name,
		Group:   group,
		Enabled: new(enabled),
		Settings: &MethodSettings{
			Subscription: &Subscription{
				IsSubscribe: true,
			},
		},
	}
}
