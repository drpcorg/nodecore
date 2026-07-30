package upstreams

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/stretchr/testify/assert"
)

// local stub: pkg/test_utils/mocks imports this package, so it can't be used here
type stubMethods struct {
	names mapset.Set[string]
}

func stubMethodsWith(methods ...string) stubMethods {
	return stubMethods{names: mapset.NewThreadUnsafeSet[string](methods...)}
}

func (s stubMethods) GetSupportedMethods() mapset.Set[string] { return s.names.Clone() }
func (s stubMethods) HasMethod(method string) bool            { return s.names.ContainsOne(method) }
func (s stubMethods) GetMethod(string) *specs.Method          { return nil }

func TestMethodsHashStableAndOrderIndependent(t *testing.T) {
	// golden value freezes the id format ("<client_type>:<methods_hash8>")
	assert.Equal(t, "3d801bd0", methodsHash(stubMethodsWith("eth_call", "eth_getLogs")))
	assert.Equal(t, "3d801bd0", methodsHash(stubMethodsWith("eth_getLogs", "eth_call")))

	assert.NotEqual(t,
		methodsHash(stubMethodsWith("eth_call", "eth_getLogs")),
		methodsHash(stubMethodsWith("eth_call")),
	)

	assert.Equal(t, "e3b0c442", methodsHash(stubMethodsWith()))
	assert.Equal(t, "e3b0c442", methodsHash(nil))
}

func TestGroupKeyOf(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "erigon")

	key := groupKeyOf(&state)
	assert.Equal(t, GroupKey{ClientType: "erigon", MethodsHash: "3d801bd0"}, key)
	assert.Equal(t, "erigon:3d801bd0", key.Id())
}

func TestGroupKeyOfFallsBackToUnknownClientType(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	assert.Equal(t, "unknown:3d801bd0", groupKeyOf(&state).Id())

	state.Labels = nil
	assert.Equal(t, "unknown:3d801bd0", groupKeyOf(&state).Id())
}

func TestGroupKeyIgnoresCaps(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "geth")
	keyWithoutCaps := groupKeyOf(&state)

	state.Caps = mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap)
	assert.Equal(t, keyWithoutCaps, groupKeyOf(&state))
}
