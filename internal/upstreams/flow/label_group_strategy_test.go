package flow_test

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/stretchr/testify/assert"
)

func TestLabelGroupStrategyExhaustsGroupBeforeNext(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id2", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id3", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil, chains.ARBITRUM)

	strategy := flow.NewLabelGroupStrategyWithGroups([][]string{{"id1", "id2"}, {"id3"}}, false, chSup)

	// pass-on-error=false: group 0 is fully exhausted before group 1 is reached
	for _, expected := range []string{"id1", "id2", "id3"} {
		up, err := strategy.SelectUpstream(request)
		assert.NoError(t, err)
		assert.Equal(t, expected, up)
	}
	_, err := strategy.SelectUpstream(request)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestLabelGroupStrategyDeadGroupFallsThrough(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	test_utils.PublishEvent(chSup, "id1", protocol.Unavailable, mapset.NewThreadUnsafeSet[protocol.Cap]())
	test_utils.PublishEvent(chSup, "id2", protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil, chains.ARBITRUM)

	// pass-on-error=false, but an entirely-dead group 0 still falls through to group 1
	strategy := flow.NewLabelGroupStrategyWithGroups([][]string{{"id1"}, {"id2"}}, false, chSup)

	up, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "id2", up)
}

func TestLabelGroupStrategyPassOnErrorJumpsGroup(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	for _, id := range []string{"id1", "id2", "id3", "id4"} {
		test_utils.PublishEvent(chSup, id, protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	}
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil, chains.ARBITRUM)

	// pass-on-error=true: each error jumps to the next group, skipping untried upstreams
	strategy := flow.NewLabelGroupStrategyWithGroups([][]string{{"id1", "id2"}, {"id3", "id4"}}, true, chSup)

	up, err := strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "id1", up) // best of group 0

	up, err = strategy.SelectUpstream(request)
	assert.NoError(t, err)
	assert.Equal(t, "id3", up) // jumped to group 1, skipping id2

	_, err = strategy.SelectUpstream(request)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err) // no groups left
}

func TestLabelGroupStrategySelectsUpstreamOnceAcrossGroups(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	for _, id := range []string{"id1", "id2", "id3"} {
		test_utils.PublishEvent(chSup, id, protocol.Available, mapset.NewThreadUnsafeSet[protocol.Cap]())
	}
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil, chains.ARBITRUM)

	// id1 belongs to both groups but must be selected only once
	strategy := flow.NewLabelGroupStrategyWithGroups([][]string{{"id1", "id2"}, {"id1", "id3"}}, false, chSup)

	for _, expected := range []string{"id1", "id2", "id3"} {
		up, err := strategy.SelectUpstream(request)
		assert.NoError(t, err)
		assert.Equal(t, expected, up)
	}
	_, err := strategy.SelectUpstream(request)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestLabelGroupStrategyNoGroups(t *testing.T) {
	chSup := test_utils.CreateChainSupervisor()
	request, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", nil, chains.ARBITRUM)
	strategy := flow.NewLabelGroupStrategyWithGroups(nil, false, chSup)

	_, err := strategy.SelectUpstream(request)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestPartitionLabelGroups(t *testing.T) {
	labels := map[string]mapset.Set[string]{
		"id1": mapset.NewThreadUnsafeSet("full", "fast"),
		"id2": mapset.NewThreadUnsafeSet("archive"),
		"id3": mapset.NewThreadUnsafeSet[string](),
	}
	labelsOf := func(id string) mapset.Set[string] { return labels[id] }
	sorted := []string{"id1", "id2", "id3"}

	withDefault := flow.PartitionLabelGroups(sorted, []string{"full", "archive"}, true, labelsOf)
	assert.Equal(t, [][]string{{"id1"}, {"id2"}, {"id3"}}, withDefault)

	withoutDefault := flow.PartitionLabelGroups(sorted, []string{"full", "archive"}, false, labelsOf)
	assert.Equal(t, [][]string{{"id1"}, {"id2"}}, withoutDefault)
}
