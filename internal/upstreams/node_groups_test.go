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
	// golden value freezes the id format ("<client_type>:<labels_hash8>:<methods_hash8>")
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

func TestGroupKeyOf(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "erigon")

	key := GroupKeyOf(&state)
	assert.Equal(t, "erigon", key.ClientType)
	assert.Equal(t, "3d801bd0", key.MethodsHash)
	assert.Equal(t, "erigon:"+key.LabelsHash+":3d801bd0", key.Id())
}

func TestGroupKeySplitsByAnyRoutingLabel(t *testing.T) {
	archive := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	archive.Labels.AddLabel("client_type", "erigon")
	archive.Labels.AddLabel("archive", "true")

	pruned := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	pruned.Labels.AddLabel("client_type", "erigon")

	// same client, same methods, different capability label: different groups,
	// otherwise the group would advertise archive it only half has
	assert.NotEqual(t, GroupKeyOf(&archive).Id(), GroupKeyOf(&pruned).Id())
}

func TestGroupKeyOfFallsBackToUnknownClientType(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call", "eth_getLogs"), nil, "", nil, nil)
	assert.Equal(t, "unknown:e3b0c442:3d801bd0", GroupKeyOf(&state).Id())

	state.Labels = nil
	assert.Equal(t, "unknown:e3b0c442:3d801bd0", GroupKeyOf(&state).Id())
}

func TestGroupKeyIgnoresCaps(t *testing.T) {
	state := protocol.DefaultUpstreamState(stubMethodsWith("eth_call"), nil, "", nil, nil)
	state.Labels.AddLabel("client_type", "geth")
	keyWithoutCaps := GroupKeyOf(&state)

	state.Caps = mapset.NewThreadUnsafeSet(protocol.WsCap, protocol.NewHeadsCap)
	assert.Equal(t, keyWithoutCaps, GroupKeyOf(&state))
}
