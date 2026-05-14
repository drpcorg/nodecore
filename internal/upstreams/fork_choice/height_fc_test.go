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

func TestHeightForkChoice_AvailableLowerHeight_UpdatesMaxDown(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	headA := protocol.NewBlock(101, 0, blockchain.NewHashIdFromString("a1"), blockchain.NewHashIdFromString("a0"))
	updated, chosen := fc.Choose("A", getStateEventType(protocol.Available, headA))
	assert.True(t, updated)
	assert.Equal(t, headA, chosen)

	headB := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("b1"), blockchain.NewHashIdFromString("b0"))
	updated, chosen = fc.Choose("B", getStateEventType(protocol.Available, headB))
	assert.False(t, updated)
	assert.Equal(t, headA, chosen)

	// A reports a lower height than its previous one (reorg / rollback / primary peer switch).
	headALower := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("a1-fork"), blockchain.NewHashIdFromString("a0-fork"))
	updated, chosen = fc.Choose("A", getStateEventType(protocol.Available, headALower))
	assert.True(t, updated)
	// New max is whichever live entry is the highest; A and B are both at 100 now.
	// max picker iterates the map, so we only assert the resulting height.
	assert.Equal(t, uint64(100), chosen.Height)
}

func TestHeightForkChoice_SingleUpstreamLowerHeight_UpdatesMaxDown(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	high := protocol.NewBlock(101, 0, blockchain.NewHashIdFromString("h1"), blockchain.NewHashIdFromString("h0"))
	updated, chosen := fc.Choose("A", getStateEventType(protocol.Available, high))
	assert.True(t, updated)
	assert.Equal(t, high, chosen)

	low := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("l1"), blockchain.NewHashIdFromString("l0"))
	updated, chosen = fc.Choose("A", getStateEventType(protocol.Available, low))
	assert.True(t, updated)
	assert.Equal(t, low, chosen)
}

func TestHeightForkChoice_RecoveryAfterLowerHeight_GoesUp(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	high := protocol.NewBlock(101, 0, blockchain.NewHashIdFromString("h1"), blockchain.NewHashIdFromString("h0"))
	_, _ = fc.Choose("A", getStateEventType(protocol.Available, high))

	low := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("l1"), blockchain.NewHashIdFromString("l0"))
	updated, chosen := fc.Choose("A", getStateEventType(protocol.Available, low))
	assert.True(t, updated)
	assert.Equal(t, low, chosen)

	higher := protocol.NewBlock(102, 0, blockchain.NewHashIdFromString("h2"), blockchain.NewHashIdFromString("h1"))
	updated, chosen = fc.Choose("A", getStateEventType(protocol.Available, higher))
	assert.True(t, updated)
	assert.Equal(t, higher, chosen)
}

func TestHeightForkChoice_EmptyHeadDoesNotOverwrite(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	known := protocol.NewBlock(101, 0, blockchain.NewHashIdFromString("k1"), blockchain.NewHashIdFromString("k0"))
	updated, chosen := fc.Choose("A", getStateEventType(protocol.Available, known))
	assert.True(t, updated)
	assert.Equal(t, known, chosen)

	// An Available event with an empty block must not erase the last known height for A.
	updated, chosen = fc.Choose("A", getStateEventType(protocol.Available, protocol.Block{}))
	assert.False(t, updated)
	assert.Equal(t, known, chosen)
}

func TestHeightForkChoice_UnavailableUnknownUpstream_NoOp(t *testing.T) {
	fc := choice.NewHeightForkChoice()

	headA := protocol.NewBlock(101, 0, blockchain.NewHashIdFromString("a1"), blockchain.NewHashIdFromString("a0"))
	_, _ = fc.Choose("A", getStateEventType(protocol.Available, headA))

	// An unavailable event for an upstream that was never registered must not flap state.
	updated, chosen := fc.Choose("never-seen", getStateEventType(protocol.Unavailable, protocol.Block{}))
	assert.False(t, updated)
	assert.Equal(t, headA, chosen)
}
