package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/stretchr/testify/assert"
)

func TestBlockCreation(t *testing.T) {
	block := protocol.NewBlock(
		uint64(15),
		uint64(55),
		blockchain.NewHashIdFromString("0x2345"),
		blockchain.NewHashIdFromString("0x1111"),
	)
	expectedBlock := protocol.Block{
		Height:     uint64(15),
		Slot:       uint64(55),
		Hash:       blockchain.NewHashIdFromString("0x2345"),
		ParentHash: blockchain.NewHashIdFromString("0x1111"),
	}

	assert.Equal(t, expectedBlock, block)
	assert.False(t, block.IsFullEmpty())
}

func TestNewBlockDataWithHeight(t *testing.T) {
	block := protocol.NewBlockWithHeight(uint64(55))
	expectedBlockData := protocol.Block{Height: uint64(55)}

	assert.Equal(t, expectedBlockData, block)
}

func TestBlockData(t *testing.T) {
	tests := []struct {
		name     string
		block    protocol.Block
		expected bool
	}{
		{
			name:     "empty data",
			block:    protocol.Block{},
			expected: true,
		},
		{
			name:     "data with height",
			block:    protocol.Block{Height: uint64(12)},
			expected: false,
		},
		{
			name:     "data with slot",
			block:    protocol.Block{Slot: uint64(12)},
			expected: false,
		},
		{
			name:     "data with hash",
			block:    protocol.Block{Hash: blockchain.NewHashIdFromString("0x2345")},
			expected: false,
		},
		{
			name:     "data with height and slot",
			block:    protocol.Block{Height: uint64(12), Slot: uint64(1)},
			expected: false,
		},
		{
			name:     "data with height and hash",
			block:    protocol.Block{Height: uint64(12), Hash: blockchain.NewHashIdFromString("0x2345")},
			expected: false,
		},
		{
			name:     "data with slot and hash",
			block:    protocol.Block{Slot: uint64(12), Hash: blockchain.NewHashIdFromString("0x2345")},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, test.block.IsFullEmpty())
		})
	}
}

func TestNewBlockWithHeights(t *testing.T) {
	block := protocol.NewBlockWithHeights(10, 20)

	assert.Equal(t, protocol.Block{
		Height: 10,
		Slot:   20,
	}, block)
}

func TestBlockEquals(t *testing.T) {
	block := protocol.NewBlock(
		15,
		55,
		blockchain.NewHashIdFromString("0x2345"),
		blockchain.NewHashIdFromString("0x1111"),
	)

	assert.True(t, block.Equals(block))
	assert.False(t, block.Equals(protocol.NewBlock(
		16,
		55,
		blockchain.NewHashIdFromString("0x2345"),
		blockchain.NewHashIdFromString("0x1111"),
	)))
	assert.False(t, block.Equals(protocol.NewBlock(
		15,
		56,
		blockchain.NewHashIdFromString("0x2345"),
		blockchain.NewHashIdFromString("0x1111"),
	)))
	assert.False(t, block.Equals(protocol.NewBlock(
		15,
		55,
		blockchain.NewHashIdFromString("0x9999"),
		blockchain.NewHashIdFromString("0x1111"),
	)))
	assert.False(t, block.Equals(protocol.NewBlock(
		15,
		55,
		blockchain.NewHashIdFromString("0x2345"),
		blockchain.NewHashIdFromString("0x2222"),
	)))
}

func TestBlockCompareWithHeight(t *testing.T) {
	block := protocol.NewBlockWithHeight(10)

	assert.Equal(t, -1, block.CompareWithHeight(protocol.NewBlockWithHeight(20)))
	assert.Equal(t, 0, block.CompareWithHeight(protocol.NewBlockWithHeight(10)))
	assert.Equal(t, 1, block.CompareWithHeight(protocol.NewBlockWithHeight(5)))
}

func TestBlockIsEmptyByHeight(t *testing.T) {
	assert.True(t, protocol.Block{}.IsEmptyByHeight())
	assert.True(t, protocol.Block{Slot: 10}.IsEmptyByHeight())
	assert.False(t, protocol.Block{Height: 1}.IsEmptyByHeight())
	assert.False(t, protocol.Block{Hash: blockchain.NewHashIdFromString("0x1234")}.IsEmptyByHeight())
	assert.False(t, protocol.Block{ParentHash: blockchain.NewHashIdFromString("0xabcd")}.IsEmptyByHeight())
}

func TestBlockInfo(t *testing.T) {
	blockInfo := protocol.NewBlockInfo()
	block := protocol.NewBlockWithHeights(10, 20)

	assert.True(t, protocol.Block{}.Equals(blockInfo.GetBlock(protocol.FinalizedBlock)))

	blockInfo.AddBlock(block, protocol.FinalizedBlock)

	assert.True(t, block.Equals(blockInfo.GetBlock(protocol.FinalizedBlock)))
	assert.Equal(t, map[protocol.BlockType]protocol.Block{
		protocol.FinalizedBlock: block,
	}, blockInfo.GetBlocks())
}

func TestBlockInfoCopy(t *testing.T) {
	blockInfo := protocol.NewBlockInfo()
	originalBlock := protocol.NewBlockWithHeights(10, 20)
	copiedBlock := protocol.NewBlockWithHeights(11, 21)
	blockInfo.AddBlock(originalBlock, protocol.FinalizedBlock)

	copiedInfo := blockInfo.Copy()
	copiedInfo.AddBlock(copiedBlock, protocol.FinalizedBlock)

	assert.True(t, originalBlock.Equals(blockInfo.GetBlock(protocol.FinalizedBlock)))
	assert.True(t, copiedBlock.Equals(copiedInfo.GetBlock(protocol.FinalizedBlock)))
	assert.NotSame(t, blockInfo, copiedInfo)
}

func TestLowerBoundInfo(t *testing.T) {
	lowerBoundInfo := protocol.NewLowerBoundInfo()
	bound := protocol.NewLowerBoundData(42, 100, protocol.BlockBound)

	_, ok := lowerBoundInfo.GetLowerBound(protocol.BlockBound)
	assert.False(t, ok)

	lowerBoundInfo.AddLowerBound(bound)
	result, ok := lowerBoundInfo.GetLowerBound(protocol.BlockBound)

	assert.True(t, ok)
	assert.Equal(t, bound, result)
	assert.ElementsMatch(t, []protocol.LowerBoundData{bound}, lowerBoundInfo.GetAllBounds())
}

func TestLowerBoundInfoCopy(t *testing.T) {
	lowerBoundInfo := protocol.NewLowerBoundInfo()
	originalBound := protocol.NewLowerBoundData(42, 100, protocol.BlockBound)
	copiedBound := protocol.NewLowerBoundData(43, 101, protocol.BlockBound)
	lowerBoundInfo.AddLowerBound(originalBound)

	copiedInfo := lowerBoundInfo.Copy()
	copiedInfo.AddLowerBound(copiedBound)

	result, ok := lowerBoundInfo.GetLowerBound(protocol.BlockBound)
	assert.True(t, ok)
	assert.Equal(t, originalBound, result)

	result, ok = copiedInfo.GetLowerBound(protocol.BlockBound)
	assert.True(t, ok)
	assert.Equal(t, copiedBound, result)
	assert.NotSame(t, lowerBoundInfo, copiedInfo)
}
