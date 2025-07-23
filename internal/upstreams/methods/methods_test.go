package methods_test

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestUpstreamMethodsNoSpecThenError(t *testing.T) {
	_, err := methods.NewUpstreamMethods("test", nil)

	assert.ErrorContains(t, err, "no method spec with name 'test'")
}

func TestUpstreamMethodsOnlyFromSpec(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{})
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndEnabledMethodInConfig(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{EnableMethods: []string{"newMethod"}})
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2", "newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroup(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{DisableMethods: []string{specs.DefaultMethodGroup}})
	assert.NoError(t, err)

	assert.True(t, upstreamMethods.GetSupportedMethods().IsEmpty())
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableCustomMethod(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"newMethod"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableAnotherGroup(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"trace"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableOneMethod(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{DisableMethods: []string{"test_another"}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func TestChainMethodsMergeAllDelegates(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("full")).Load()
	assert.NoError(t, err)

	methodsConfig1 := &config.MethodsConfig{DisableMethods: []string{"test2"}}
	methodsConfig2 := &config.MethodsConfig{EnableMethods: []string{"newMethod"}}

	upstreamMethods1, err := methods.NewUpstreamMethods("test", methodsConfig1)
	assert.NoError(t, err)
	upstreamMethods2, err := methods.NewUpstreamMethods("test", methodsConfig2)
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
