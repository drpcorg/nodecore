package upstreams

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/public/pkg/methods"
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
	// golden value freezes the id format ("g:<client_type>:<labels_hash8>:<methods_hash8>")
	assert.Equal(t, "3d801bd0", methodsHash(stubMethodsWith("eth_call", "eth_getLogs")))
	assert.Equal(t, "3d801bd0", methodsHash(stubMethodsWith("eth_getLogs", "eth_call")))

	assert.NotEqual(t,
		methodsHash(stubMethodsWith("eth_call", "eth_getLogs")),
		methodsHash(stubMethodsWith("eth_call")),
	)

	assert.Equal(t, "e3b0c442", methodsHash(stubMethodsWith()))
	assert.Equal(t, "e3b0c442", methodsHash(nil))
}

func TestLabelsHashStableAndOrderIndependent(t *testing.T) {
	// golden value freezes the empty case, which is also what a nil Labels gives
	assert.Equal(t, "e3b0c442", labelsHash(nil))
	assert.Equal(t, "e3b0c442", labelsHash(protocol.NewLabels()))

	one := protocol.NewLabels()
	one.AddLabel("client_type", "erigon")
	one.AddLabel("archive", "true")

	other := protocol.NewLabels()
	other.AddLabel("archive", "true")
	other.AddLabel("client_type", "erigon")

	assert.Equal(t, labelsHash(one), labelsHash(other), "insertion order must not matter")

	third := protocol.NewLabels()
	third.AddLabel("client_type", "erigon")
	assert.NotEqual(t, labelsHash(one), labelsHash(third), "a differing label must change the hash")
}

func TestLabelsHashIgnoresTheNodeGroupLabel(t *testing.T) {
	labels := protocol.NewLabels()
	labels.AddLabel("client_type", "geth")
	before := labelsHash(labels)

	// the id is derived from the labels, so letting it in would be self-referential
	labels.AddLabel(NodeGroupLabel, "geth:dead:beef")
	assert.Equal(t, before, labelsHash(labels))
}

func TestNodeGroupIdPerLevel(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "erigon")
	labels := labelsHash(state.Labels)

	assert.Equal(t, "l:erigon:"+labels, nodeGroupId(SeparationLabels, "up-1", &state))
	assert.Equal(t, "g:erigon:"+labels+":3d801bd0", nodeGroupId(SeparationGroups, "up-1", &state))
	assert.Equal(t, "n:"+upstreamHash("up-1"), nodeGroupId(SeparationFull, "up-1", &state))
	assert.Len(t, upstreamHash("up-1"), 12)

	for _, level := range SeparationLevels {
		parsed, ok := NodeGroupLevel(nodeGroupId(level, "up-1", &state))
		assert.True(t, ok)
		assert.Equal(t, level, parsed)
	}
}

func TestNodeGroupIdSplitsByAnyRoutingLabel(t *testing.T) {
	archive := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	archive.Labels.AddLabel("client_type", "erigon")
	archive.Labels.AddLabel("archive", "true")

	pruned := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	pruned.Labels.AddLabel("client_type", "erigon")

	// same client, same methods, different capability label: different groups,
	// otherwise the group would advertise archive it only half has
	assert.NotEqual(t, nodeGroupId(SeparationGroups, "up-1", &archive), nodeGroupId(SeparationGroups, "up-2", &pruned))
	assert.NotEqual(t, nodeGroupId(SeparationLabels, "up-1", &archive), nodeGroupId(SeparationLabels, "up-2", &pruned))
}

func TestNodeGroupIdFallsBackToUnknownClientType(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	assert.Equal(t, "g:unknown:e3b0c442:3d801bd0", nodeGroupId(SeparationGroups, "up-1", &state))

	state.Labels = nil
	assert.Equal(t, "g:unknown:e3b0c442:3d801bd0", nodeGroupId(SeparationGroups, "up-1", &state))
}

func TestNodeGroupIdIgnoresCaps(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "geth")
	idWithoutCaps := nodeGroupId(SeparationGroups, "up-1", &state)

	state.Caps = mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap)
	assert.Equal(t, idWithoutCaps, nodeGroupId(SeparationGroups, "up-1", &state))
}
