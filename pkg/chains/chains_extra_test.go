package chains

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validExtraYaml = `
chain-settings:
  default:
    expected-block-time: 2s
    lags:
      lagging: 5
      syncing: 10
  protocols:
    - type: eth
      settings:
        method-spec: "eth"
      chains:
        - id: BesuPrivate
          short-names: [besu-private-test]
          chain-id: "0xdeadbeef"
          grpcId: 60001
          settings:
            expected-block-time: 1s
`

const conflictExtraYaml = `
chain-settings:
  default:
    expected-block-time: 12s
  protocols:
    - type: eth
      settings:
        method-spec: "eth"
      chains:
        - id: NotEthereum
          short-names: [ethereum]
          chain-id: "0xdeadbeef"
          grpcId: 60002
`

const grpcCollisionExtraYamlTpl = `
chain-settings:
  default:
    expected-block-time: 12s
  protocols:
    - type: eth
      settings:
        method-spec: "eth"
      chains:
        - id: BesuClash
          short-names: [besu-grpc-clash]
          chain-id: "0xc0ffee"
          grpcId: %d
`

const secondLoadYaml = `
chain-settings:
  default:
    expected-block-time: 2s
  protocols:
    - type: eth
      settings:
        method-spec: "eth"
      chains:
        - id: AnotherOne
          short-names: [another-private-test]
          chain-id: "0xfeed"
          grpcId: 60002
`

// Tests that exercise validation must run BEFORE the happy-path test
// (TestLoadExtraChains_RegistersNewChain) since that one flips the
// process-wide "loaded" flag. Go runs tests within a file in source
// order, so we order the cases here intentionally.

func TestLoadExtraChains_EmptyInputIsNoop(t *testing.T) {
	before := len(GetAllChains())
	require.NoError(t, LoadExtraChains(nil))
	require.NoError(t, LoadExtraChains([]byte{}))
	assert.Equal(t, before, len(GetAllChains()))
}

func TestLoadExtraChains_InvalidYamlReturnsError(t *testing.T) {
	err := LoadExtraChains([]byte("chain-settings:\n  protocols:\n   - id: bad\n     short-names:\n      - x\n  -malformed"))
	require.Error(t, err)
}

func TestLoadExtraChains_DuplicateShortNameRejected(t *testing.T) {
	err := LoadExtraChains([]byte(conflictExtraYaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

func TestLoadExtraChains_GrpcIdCollisionRejected(t *testing.T) {
	ethereum := GetChain("ethereum")
	require.NotEqual(t, UnknownChain, ethereum)
	yaml := []byte(fmt.Sprintf(grpcCollisionExtraYamlTpl, ethereum.GrpcId))

	err := LoadExtraChains(yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grpcId")
}

func TestLoadExtraChains_RegistersNewChain(t *testing.T) {
	require.NoError(t, LoadExtraChains([]byte(validExtraYaml)))

	c := GetChain("besu-private-test")
	require.NotEqual(t, UnknownChain, c, "extra chain should be registered")
	assert.Equal(t, "0xdeadbeef", c.ChainId)
	assert.Equal(t, "eth", c.MethodSpec, "method-spec should resolve to eth via protocol type")
	assert.True(t, c.Chain >= dynamicChainBaseId, "extra chain should be allocated a dynamic Chain id")
	assert.Equal(t, "besu-private-test", c.Chain.String(), "Chain.String() should round-trip the short-name")

	byGrpc := GetChainByGrpcId(60001)
	assert.Equal(t, c, byGrpc)
}

func TestLoadExtraChains_SecondCallIsRejected(t *testing.T) {
	// Previous successful load flipped extraChainsLoadedFlag.
	err := LoadExtraChains([]byte(secondLoadYaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already")
	assert.Equal(t, UnknownChain, GetChain("another-private-test"),
		"second-load chain must NOT be registered")
}
