package fork_choice_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	choice "github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/stretchr/testify/assert"
)

func getStateEventType(status protocol.AvailabilityStatus, head protocol.Block) *protocol.HeadUpstreamEvent {
	return &protocol.HeadUpstreamEvent{
		Status: status,
		Head:   head,
	}
}

func TestChoiceHeights(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	head1 := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("1"), blockchain.NewHashIdFromString("2"))
	updated, chosenHead := fc.Choose("id1", getStateEventType(protocol.Available, head1))
	assert.True(t, updated)
	assert.Equal(t, head1, chosenHead)

	head2 := protocol.NewBlock(50, 0, blockchain.NewHashIdFromString("5"), blockchain.NewHashIdFromString("7"))
	updated, chosenHead = fc.Choose("id2", getStateEventType(protocol.Available, head2))
	assert.False(t, updated)
	assert.Equal(t, head1, chosenHead)

	head3 := protocol.NewBlock(200, 0, blockchain.NewHashIdFromString("53"), blockchain.NewHashIdFromString("74"))
	updated, chosenHead = fc.Choose("id1", getStateEventType(protocol.Available, head3))
	assert.True(t, updated)
	assert.Equal(t, head3, chosenHead)

	head4 := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("533"), blockchain.NewHashIdFromString("742"))
	updated, chosenHead = fc.Choose("id1", getStateEventType(protocol.Unavailable, head4))
	assert.True(t, updated)
	assert.Equal(t, head2, chosenHead)

	head5 := protocol.NewBlock(1000, 0, blockchain.NewHashIdFromString("5332"), blockchain.NewHashIdFromString("1742"))
	updated, chosenHead = fc.Choose("id2", getStateEventType(protocol.Unavailable, head5))
	assert.True(t, updated)
	assert.Equal(t, protocol.ZeroBlock{}, chosenHead)
}
