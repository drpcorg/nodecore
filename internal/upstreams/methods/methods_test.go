package methods_test

import (
	"os"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamMethodsNoSpecThenError(t *testing.T) {
	_, err := methods.NewUpstreamMethods("missing-method-spec", nil, nil)

	assert.ErrorContains(t, err, "no method spec with name 'missing-method-spec'")
}

func TestUpstreamMethodsOnlyFromSpec(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{}, nil)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndEnabledMethodInConfig(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{EnableMethods: []string{"newMethod"}}, nil)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2", "newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroup(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{DisableMethods: []string{specs.DefaultMethodGroup}}, nil)
	assert.NoError(t, err)

	assert.True(t, upstreamMethods.GetSupportedMethods().IsEmpty())
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableCustomMethod(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"newMethod"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig, nil)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableAnotherGroup(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"trace"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig, nil)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableOneMethod(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{DisableMethods: []string{"test_another"}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig, nil)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func TestChainMethodsMergeAllDelegates(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig1 := &config.MethodsConfig{DisableMethods: []string{"test2"}}
	methodsConfig2 := &config.MethodsConfig{EnableMethods: []string{"newMethod"}}

	upstreamMethods1, err := methods.NewUpstreamMethods("test", methodsConfig1, nil)
	assert.NoError(t, err)
	upstreamMethods2, err := methods.NewUpstreamMethods("test", methodsConfig2, nil)
	assert.NoError(t, err)

	chainMethods := methods.NewChainMethods([]methods.Methods{upstreamMethods1, upstreamMethods2})

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "newMethod", "test2")
	checkMethods(t, expected, chainMethods)
}

func checkMethods(t *testing.T, expected mapset.Set[string], methods methods.Methods) {
	assert.Equal(t, expected, methods.GetSupportedMethods())

	for _, methodName := range expected.ToSlice() {
		method := methods.GetMethod(methodName)

		assert.NotNil(t, method)
		assert.Equal(t, methodName, method.Name)
		assert.True(t, methods.HasMethod(methodName))
	}
}

func TestIsForceEnabled(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load())

	// "test" and "test_another" are in group "trace"; "test2" is in "super-group".
	traceMethod := specs.GetSpecMethod("test", "test")
	require.NotNil(t, traceMethod)
	otherMethod := specs.GetSpecMethod("test", "test2")
	require.NotNil(t, otherMethod)

	tests := []struct {
		name     string
		enabled  []string
		method   *specs.Method
		expected bool
	}{
		{name: "nil config", enabled: nil, method: traceMethod, expected: false},
		{name: "unrelated entry", enabled: []string{"something_else"}, method: traceMethod, expected: false},
		{name: "exact method name", enabled: []string{"test"}, method: traceMethod, expected: true},
		{name: "own group", enabled: []string{"trace"}, method: traceMethod, expected: true},
		{name: "another method's group", enabled: []string{"trace"}, method: otherMethod, expected: false},
		{name: "the synthetic default group covers everything", enabled: []string{specs.DefaultMethodGroup}, method: otherMethod, expected: true},
		{name: "a non-subscription is not covered by the sub group", enabled: []string{specs.SubMethodGroup}, method: traceMethod, expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var methodsConfig *config.MethodsConfig
			if test.enabled != nil {
				methodsConfig = &config.MethodsConfig{EnableMethods: test.enabled}
			}
			assert.Equal(t, test.expected, methods.IsForceEnabled(methodsConfig, test.method))
		})
	}
}

func TestIsForceEnabledNilMethod(t *testing.T) {
	assert.False(t, methods.IsForceEnabled(&config.MethodsConfig{EnableMethods: []string{"test"}}, nil))
}
