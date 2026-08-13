package methods_test

import (
	"os"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectableMethodsExcludesLocalMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("detectable")).Load())

	detectable := methods.DetectableMethods("detectable", []specs.ApiConnectorType{specs.JsonRpcConnector})

	expected := mapset.NewThreadUnsafeSet[string]("chain_getBalance", "trace_block")
	assert.True(t, expected.Equal(detectable), "expected %v, got %v", expected.ToSlice(), detectable.ToSlice())
	assert.False(t, detectable.ContainsOne("chain_chainId"), "a locally-served method must never be detectable")
}

func TestDetectableMethodsWithoutConnectorsIsEmpty(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("detectable")).Load())

	detectable := methods.DetectableMethods("detectable", nil)

	assert.True(t, detectable.IsEmpty(), "no connector means nothing to detect, not every method")
}

func TestDetectableMethodsUnknownSpecIsEmpty(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("detectable")).Load())

	detectable := methods.DetectableMethods("no-such-spec", []specs.ApiConnectorType{specs.JsonRpcConnector})

	assert.True(t, detectable.IsEmpty())
}
