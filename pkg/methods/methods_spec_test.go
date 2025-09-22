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
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/full")).Load()
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
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/same_names")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: spec with name 'test' already exists")
}

func TestLoadSpecEmptyDirThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty")).Load()

	assert.ErrorContains(t, err, "no method specs")
}

func TestLoadSpecEmptyNameThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_name")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty spec name, file - 'spec1.json'")
}

func TestLoadSpecEmptySpecDataThenError(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/empty_spec_data")).Load()

	assert.ErrorContains(t, err, "couldn't read method specs: empty spec name, file - 'spec1.json'")
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

func TestLoadSpecMergeMethods(t *testing.T) {
	err := specs.NewMethodSpecLoaderWithFs(os.DirFS("test_specs/merge_methods")).Load()

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
