package fork_choice_test

import (
	"testing"

	"github.com/drpcorg/dsheltie/internal/protocol"
	choice "github.com/drpcorg/dsheltie/internal/upstreams/fork_choice"
	"github.com/stretchr/testify/assert"
)

func createEvent(id string, status protocol.AvailabilityStatus, height uint64) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id: id,
		State: &protocol.UpstreamState{
			Status: status,
			HeadData: &protocol.BlockData{
				Height: height,
			},
		},
	}
}

func TestChoiceHeights(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	updated, height := fc.Choose(createEvent("id1", protocol.Available, 100))
	assert.True(t, updated)
	assert.Equal(t, uint64(100), height)

	updated, height = fc.Choose(createEvent("id2", protocol.Available, 50))
	assert.False(t, updated)
	assert.Equal(t, uint64(100), height)

	updated, height = fc.Choose(createEvent("id1", protocol.Available, 200))
	assert.True(t, updated)
	assert.Equal(t, uint64(200), height)

	updated, height = fc.Choose(createEvent("id1", protocol.Unavailable, 500))
	assert.True(t, updated)
	assert.Equal(t, uint64(50), height)

	updated, height = fc.Choose(createEvent("id2", protocol.Unavailable, 1000))
	assert.True(t, updated)
	assert.Equal(t, uint64(0), height)
}
