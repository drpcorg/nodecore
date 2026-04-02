package emerald_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubMethodsToApi(t *testing.T) {
	event := emerald.SubMethodsToApi([]string{"eth_subscribe", "newHeads"})

	subsEvent := event.GetSupportedSubscriptionsEvent()
	require.NotNil(t, subsEvent)
	assert.Equal(t, []string{"eth_subscribe", "newHeads"}, subsEvent.Subs)
}

func TestLabelsToApi(t *testing.T) {
	event := emerald.LabelsToApi([]upstreams.AggregatedLabels{
		upstreams.NewAggregatedLabels(2, map[string]string{
			"client_type":    "solana",
			"client_version": "1.18.23",
		}),
	})

	nodesEvent := event.GetNodesEvent()
	require.NotNil(t, nodesEvent)
	require.Len(t, nodesEvent.Nodes, 1)
	assert.Equal(t, uint32(2), nodesEvent.Nodes[0].Quorum)
	assert.ElementsMatch(t, []*dshackle.Label{
		{Name: "client_type", Value: "solana"},
		{Name: "client_version", Value: "1.18.23"},
	}, nodesEvent.Nodes[0].Labels)
}

func TestChainStatusToApi(t *testing.T) {
	tests := []struct {
		name     string
		status   protocol.AvailabilityStatus
		expected dshackle.AvailabilityEnum
	}{
		{name: "available", status: protocol.Available, expected: dshackle.AvailabilityEnum_AVAIL_OK},
		{name: "unavailable", status: protocol.Unavailable, expected: dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE},
		{name: "immature", status: protocol.Immature, expected: dshackle.AvailabilityEnum_AVAIL_IMMATURE},
		{name: "syncing", status: protocol.Syncing, expected: dshackle.AvailabilityEnum_AVAIL_SYNCING},
		{name: "unknown", status: protocol.UnknownStatus, expected: dshackle.AvailabilityEnum_AVAIL_UNKNOWN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := emerald.ChainStatusToApi(tt.status)

			statusEvent := event.GetStatus()
			require.NotNil(t, statusEvent)
			assert.Equal(t, tt.expected, statusEvent.Availability)
		})
	}
}

func TestSupportedMethodsToApi(t *testing.T) {
	event := emerald.SupportedMethodsToApi([]string{"eth_call", "eth_getBalance"})

	methodsEvent := event.GetSupportedMethodsEvent()
	require.NotNil(t, methodsEvent)
	assert.Equal(t, []string{"eth_call", "eth_getBalance"}, methodsEvent.Methods)
}

func TestLowerBoundsToApi(t *testing.T) {
	event := emerald.LowerBoundsToApi([]protocol.LowerBoundData{
		protocol.NewLowerBoundData(10, 100, protocol.TxBound),
		protocol.NewLowerBoundData(20, 200, protocol.SlotBound),
		protocol.NewLowerBoundData(30, 300, protocol.StateBound),
		protocol.NewLowerBoundData(40, 400, protocol.ReceiptsBound),
		protocol.NewLowerBoundData(50, 500, protocol.BlockBound),
		protocol.NewLowerBoundData(60, 600, protocol.UnknownBound),
	})

	lowerBoundsEvent := event.GetLowerBoundsEvent()
	require.NotNil(t, lowerBoundsEvent)
	assert.ElementsMatch(t, []*dshackle.LowerBound{
		{LowerBoundTimestamp: 100, LowerBoundValue: 10, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_TX},
		{LowerBoundTimestamp: 200, LowerBoundValue: 20, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_SLOT},
		{LowerBoundTimestamp: 300, LowerBoundValue: 30, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_STATE},
		{LowerBoundTimestamp: 400, LowerBoundValue: 40, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_RECEIPTS},
		{LowerBoundTimestamp: 500, LowerBoundValue: 50, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_BLOCK},
		{LowerBoundTimestamp: 600, LowerBoundValue: 60, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_UNSPECIFIED},
	}, lowerBoundsEvent.LowerBounds)
}

func TestBlocksToApi(t *testing.T) {
	event := emerald.BlocksToApi(map[protocol.BlockType]protocol.Block{
		protocol.FinalizedBlock: protocol.NewBlockWithHeight(123),
		protocol.BlockType(99):  protocol.NewBlockWithHeight(456),
	})

	finalizationEvent := event.GetFinalizationDataEvent()
	require.NotNil(t, finalizationEvent)
	assert.ElementsMatch(t, []*dshackle.FinalizationData{
		{Height: 123, Type: dshackle.FinalizationType_FINALIZATION_FINALIZED_BLOCK},
		{Height: 456, Type: dshackle.FinalizationType_FINALIZATION_UNSPECIFIED},
	}, finalizationEvent.FinalizationData)
}

func TestHeadToApi(t *testing.T) {
	head := protocol.NewBlock(
		100,
		200,
		blockchain.NewHashIdFromString("0xabc"),
		blockchain.NewHashIdFromString("0xdef"),
	)

	event := emerald.HeadToApi(head)

	headEvent := event.GetHead()
	require.NotNil(t, headEvent)
	assert.Equal(t, uint64(100), headEvent.Height)
	assert.Equal(t, uint64(200), headEvent.Slot)
	assert.Equal(t, head.Hash.ToHex(), headEvent.BlockId)
	assert.Equal(t, head.ParentHash.ToHex(), headEvent.ParentBlockId)
}
