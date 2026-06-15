package specs_test

import (
	"testing"
	"testing/fstest"

	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// a self-contained "plain" spec that does not clash with any embedded spec
const extraSpecJSON = `{
  "openrpc": "1.0.0",
  "info": {"title": "private besu methods", "version": "1.0.0"},
  "spec": {
    "name": "private-besu",
    "api-connectors": ["json-rpc"],
    "type": "plain"
  },
  "methods": [
    {
      "name": "qbft_getValidatorsByBlockNumber",
      "group": "default",
      "params": [],
      "tag-parser": {"type": "blockNumber", "path": ".[0]"}
    }
  ]
}`

// a spec that reuses the embedded "eth" name, which must be rejected
const clashingSpecJSON = `{
  "openrpc": "1.0.0",
  "info": {"title": "clashing", "version": "1.0.0"},
  "spec": {
    "name": "eth",
    "api-connectors": ["json-rpc"],
    "type": "plain"
  },
  "methods": [
    {"name": "eth_somethingCustom", "group": "default", "params": []}
  ]
}`

// Extra specs extend the embedded registry: a new spec name loads alongside
// the built-ins and its methods become queryable.
func TestExtraSpecsExtendEmbedded(t *testing.T) {
	extra := fstest.MapFS{
		"private-besu.json": {Data: []byte(extraSpecJSON)},
	}

	err := specs.NewMethodSpecLoaderWithExtraFs(extra).Load()
	require.NoError(t, err)

	// embedded specs are still present
	ethSpec := specs.GetSpecMethodsByConnectors("eth", nil)
	assert.NotEmpty(t, ethSpec)

	// the extra spec was added
	besuSpec := specs.GetSpecMethodsByConnectors("private-besu", nil)
	require.NotEmpty(t, besuSpec)
	assert.Contains(t, besuSpec[specs.DefaultMethodGroup], "qbft_getValidatorsByBlockNumber")
}

// An extra spec may not silently replace an embedded one: reusing an existing
// spec name is rejected (strict add-only, same as extra chains).
func TestExtraSpecsCannotOverrideEmbedded(t *testing.T) {
	extra := fstest.MapFS{
		"eth-override.json": {Data: []byte(clashingSpecJSON)},
	}

	err := specs.NewMethodSpecLoaderWithExtraFs(extra).Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}
