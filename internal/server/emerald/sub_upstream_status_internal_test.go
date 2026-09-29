package emerald

import (
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedUpstreamsChainSupervisor struct {
	upstreams.ChainSupervisor
	ids    []string
	states map[string]*protocol.UpstreamState
}

func (s *fixedUpstreamsChainSupervisor) GetUpstreamIds() []string { return s.ids }
func (s *fixedUpstreamsChainSupervisor) GetUpstreamState(id string) *protocol.UpstreamState {
	return s.states[id]
}

// a pass that finds nothing changed reuses what the chain keeps
func TestUpstreamStatusPassWithoutChangesAllocatesNothing(t *testing.T) {
	chainSupervisor := &fixedUpstreamsChainSupervisor{ids: []string{"a", "b"}, states: make(map[string]*protocol.UpstreamState)}
	for _, id := range chainSupervisor.ids {
		state := protocol.DefaultUpstreamState(nil, nil, "", nil, nil)
		chainSupervisor.states[id] = &state
	}
	chain := &upstreamStatusChain{subMethods: mapset.NewThreadUnsafeSet[string](), sent: make(map[string]sentUpstream)}
	now := time.Now()
	require.NotNil(t, chain.response(chainSupervisor, now, time.Hour))

	assert.Zero(t, testing.AllocsPerRun(100, func() {
		if chain.response(chainSupervisor, now, time.Hour) != nil {
			t.Fatal("nothing changed")
		}
	}))
}
