package specs_test

import (
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestLoadSpecAndCheckGroupsAndDefaultParams(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/full")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("test")

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
	t.Setenv(specs.SpecPathVar, "test_specs/full")
	err := specs.Load()
	assert.NoError(t, err)

	spec := specs.GetSpecMethods("another_test")

	defaultGroup, ok := spec[specs.DefaultMethodGroup]
	assert.True(t, ok)
	assert.Len(t, defaultGroup, 1)

	method1 := defaultGroup["test"]
	assert.False(t, method1.IsCacheable())
	assert.True(t, method1.Enabled())
}

func TestLoadSpecWithTheSameNameThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/same_names")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: spec with name 'test' already exists")
}

func TestLoadSpecEmptyDirThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/empty")
	err := specs.Load()

	assert.ErrorContains(t, err, "no method specs, path 'test_specs/empty'")
}

func TestLoadSpecEmptyNameThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/empty_name")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty spec name, file - 'test_specs/empty_name/spec1.json'")
}

func TestLoadSpecEmptySpecDataThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/empty_spec_data")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty spec name, file - 'test_specs/empty_spec_data/spec1.json'")
}

func TestLoadSpecEmptyMethodNameThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/empty_method_name")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty method name, file - 'test_specs/empty_method_name/spec1.json', index - 0")
}

func TestLoadSpecEmptyParserPathThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/empty_parser_path")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'test' of 'test_specs/empty_parser_path/spec1.json' validation, cause: empty tag-parser path")
}

func TestLoadSpecWrongParserReturnTypeThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/wrong_parser_return_type")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'test' of 'test_specs/wrong_parser_return_type/spec1.json' validation, cause: wrong return type of tag-parser - wrong")
}

func TestLoadSpecExistedMethodThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/existed_method")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: method 'test_another' already exists, file - 'test_specs/existed_method/spec1.json'")
}

func TestLoadSpecWrongJqPathThenError(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/wrong_parser_path")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't merge method specs: spec 'test', error 'couldn't parse a jq path of method test - unexpected token \"!\"'")
}

func TestLoadSpecWrongStickySettings(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/wrong_sticky")
	err := specs.Load()

	assert.ErrorContains(t, err, "couldn't read method specs: error during method 'eth_uninstallFilter' of 'test_specs/wrong_sticky/spec.json' validation, cause: both 'create-sticky' and 'send-sticky' are enabled")
}

func TestLoadSpecMergeMethods(t *testing.T) {
	t.Setenv(specs.SpecPathVar, "test_specs/merge_methods")
	err := specs.Load()

	assert.NoError(t, err)

	spec1 := specs.GetSpecMethods("another")
	assert.Equal(t, 4, len(spec1))

	spec2 := specs.GetSpecMethods("test")
	assert.Equal(t, 5, len(spec2))

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

func TestLoadSpecsAndMergeMethods(t *testing.T) {

}
