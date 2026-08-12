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

func TestDetectableMethodsExcludesLocalMethods(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("detectable")).Load())

	detectable := methods.DetectableMethods("detectable", []specs.ApiConnectorType{specs.JsonRpcConnector})

	expected := mapset.NewThreadUnsafeSet[string]("chain_getBalance", "trace_block")
	assert.True(t, expected.Equal(detectable), "expected %v, got %v", expected.ToSlice(), detectable.ToSlice())
	assert.False(t, detectable.ContainsOne("chain_chainId"), "a locally-served method must never be detectable")
}

func TestDetectableMethodsUnknownSpecIsEmpty(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoaderWithFs(os.DirFS("detectable")).Load())

	detectable := methods.DetectableMethods("no-such-spec", []specs.ApiConnectorType{specs.JsonRpcConnector})

	assert.True(t, detectable.IsEmpty())
}

func TestIsExplicitlyEnabled(t *testing.T) {
	methodsConfig := &config.MethodsConfig{EnableMethods: []string{"trace_block"}}

	assert.True(t, methods.IsExplicitlyEnabled(methodsConfig, "trace_block"))
	assert.False(t, methods.IsExplicitlyEnabled(methodsConfig, "debug_traceCall"))
	assert.False(t, methods.IsExplicitlyEnabled(nil, "trace_block"))
}
