package methods_test

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestUpstreamMethodsNoSpecThenError(t *testing.T) {
	_, err := methods.NewUpstreamMethods("test", nil)

	assert.ErrorContains(t, err, "no method spec with name 'test'")
}

func TestUpstreamMethodsOnlyFromSpec(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{})
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndEnabledMethodInConfig(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{EnableMethods: []string{"newMethod"}})
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another", "test2", "newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroup(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	upstreamMethods, err := methods.NewUpstreamMethods("test", &config.MethodsConfig{DisableMethods: []string{specs.DefaultMethodGroup}})
	assert.NoError(t, err)

	assert.True(t, upstreamMethods.GetSupportedMethods().IsEmpty())
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableCustomMethod(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"newMethod"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("newMethod")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableDefaultGroupAndEnableAnotherGroup(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"trace"}, DisableMethods: []string{specs.DefaultMethodGroup}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test_another")
	checkMethods(t, expected, upstreamMethods)
}

func TestUpstreamMethodsAndDisableOneMethod(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "full")
	err := specs.Load()
	assert.NoError(t, err)

	methodsConfig := &config.MethodsConfig{DisableMethods: []string{"test_another"}}

	upstreamMethods, err := methods.NewUpstreamMethods("test", methodsConfig)
	assert.NoError(t, err)

	expected := mapset.NewThreadUnsafeSet[string]("test", "test2")
	checkMethods(t, expected, upstreamMethods)
}

func checkMethods(t *testing.T, expected mapset.Set[string], upstreamMethods *methods.UpstreamMethods) {
	assert.Equal(t, expected, upstreamMethods.GetSupportedMethods())

	for _, methodName := range expected.ToSlice() {
		assert.True(t, upstreamMethods.HasMethod(methodName))
	}
}
